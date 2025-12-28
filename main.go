package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apialerts/apialerts-go"
	"github.com/tarm/serial"
)

const (
	// Configuration
	defaultWarningThreshold  = 70.0             // GPU warning threshold in Celsius
	defaultCriticalThreshold = 80.0             // GPU critical threshold in Celsius
	defaultAmbientThreshold  = 24.0             // DHT22 ambient warning threshold
	defaultCheckInterval     = 5                // Check interval in seconds
	defaultChannel           = "gpu-monitoring" // Default API Alerts channel
	defaultSerialPort        = "/dev/ttyUSB0"   // Default Arduino serial port
	defaultSerialBaud        = 57600            // Arduino baud rate
)

type Config struct {
	APIKey            string
	Channel           string
	WarningThreshold  float64
	CriticalThreshold float64
	AmbientThreshold  float64
	CheckInterval     time.Duration
	SerialPort        string
	SerialBaud        int
}

type GPUTemperature struct {
	Index       int
	Temperature float64
	Name        string
}

type DHT22Reading struct {
	Temperature float64
	Humidity    float64
}

func loadConfig() Config {
	// Load from environment variables with defaults
	apiKey := os.Getenv("APIALERTS_API_KEY")
	if apiKey == "" {
		log.Fatal("APIALERTS_API_KEY environment variable is required")
	}

	channel := os.Getenv("APIALERTS_CHANNEL")
	if channel == "" {
		channel = defaultChannel
	}

	warningThreshold := defaultWarningThreshold
	if t := os.Getenv("GPU_TEMP_WARNING"); t != "" {
		if parsed, err := strconv.ParseFloat(t, 64); err == nil {
			warningThreshold = parsed
		}
	}

	criticalThreshold := defaultCriticalThreshold
	if t := os.Getenv("GPU_TEMP_CRITICAL"); t != "" {
		if parsed, err := strconv.ParseFloat(t, 64); err == nil {
			criticalThreshold = parsed
		}
	}

	ambientThreshold := defaultAmbientThreshold
	if t := os.Getenv("AMBIENT_TEMP_THRESHOLD"); t != "" {
		if parsed, err := strconv.ParseFloat(t, 64); err == nil {
			ambientThreshold = parsed
		}
	}

	interval := defaultCheckInterval
	if i := os.Getenv("GPU_CHECK_INTERVAL"); i != "" {
		if parsed, err := strconv.Atoi(i); err == nil {
			interval = parsed
		}
	}

	serialPort := os.Getenv("ARDUINO_SERIAL_PORT")
	if serialPort == "" {
		serialPort = defaultSerialPort
	}

	serialBaud := defaultSerialBaud
	if b := os.Getenv("ARDUINO_SERIAL_BAUD"); b != "" {
		if parsed, err := strconv.Atoi(b); err == nil {
			serialBaud = parsed
		}
	}

	return Config{
		APIKey:            apiKey,
		Channel:           channel,
		WarningThreshold:  warningThreshold,
		CriticalThreshold: criticalThreshold,
		AmbientThreshold:  ambientThreshold,
		CheckInterval:     time.Duration(interval) * time.Second,
		SerialPort:        serialPort,
		SerialBaud:        serialBaud,
	}
}

// detectArduino attempts to find and connect to an Arduino
func detectArduino(baud int) (*serial.Port, string, error) {
	log.Println("Auto-detecting Arduino...")

	// Common Arduino serial device patterns on Linux (prioritize USB devices)
	patterns := []string{
		"/dev/ttyACM*", // Arduino Uno, Mega (native USB)
		"/dev/ttyUSB*", // USB-to-serial adapters
	}

	var candidates []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		candidates = append(candidates, matches...)
	}

	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("no USB serial devices found (no /dev/ttyUSB* or /dev/ttyACM* devices)")
	}

	log.Printf("Found %d potential serial devices: %v", len(candidates), candidates)

	// Try each device
	for _, device := range candidates {
		log.Printf("  Trying %s...", device)

		config := &serial.Config{
			Name:        device,
			Baud:        baud,
			ReadTimeout: time.Millisecond * 500,
		}

		port, err := serial.OpenPort(config)
		if err != nil {
			log.Printf("    Failed to open: %v", err)
			continue
		}

		log.Printf("    Port opened, waiting for Arduino reset...")
		// Wait for Arduino to reset after serial connection
		time.Sleep(2 * time.Second)

		// Flush any existing data
		buf := make([]byte, 1024)
		port.Read(buf)

		// Send a test command to see if Arduino responds
		log.Printf("    Sending test command...")
		_, err = port.Write([]byte("FAN:50\n"))
		if err != nil {
			log.Printf("    Failed to write test command: %v", err)
			port.Close()
			continue
		}

		// Try to read Arduino messages
		scanner := bufio.NewScanner(port)

		foundArduino := false
		deadline := time.Now().Add(5 * time.Second)

		for time.Now().Before(deadline) && !foundArduino {
			if scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					log.Printf("    Received: %s", line)

					// Check for Arduino fan controller signature
					if strings.Contains(line, "Fan controller started") ||
						strings.Contains(line, "MSG:T:") ||
						strings.Contains(line, "FAN:") ||
						strings.Contains(line, "ERR:TEMP_READ_FAIL") {
						log.Printf("    ✓ Arduino fan controller detected!")
						return port, device, nil
					}
				}
			}

			// Small delay between reads
			time.Sleep(100 * time.Millisecond)
		}

		log.Printf("    No Arduino response within timeout")
		port.Close()
	}

	return nil, "", fmt.Errorf("no Arduino found on any serial port - checked: %v", candidates)
}

// getGPUTemperatures retrieves temperatures using nvidia-smi
func getGPUTemperatures() ([]GPUTemperature, error) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=index,temperature.gpu,name",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run nvidia-smi: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	temps := make([]GPUTemperature, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		index, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			continue
		}

		temp, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			continue
		}

		name := strings.TrimSpace(fields[2])

		temps = append(temps, GPUTemperature{
			Index:       index,
			Temperature: temp,
			Name:        name,
		})
	}

	return temps, nil
}

// parseDHT22Message parses Arduino messages like "MSG:T:225-H:550"
// Temperature is in tenths (225 = 22.5°C), Humidity is in tenths (550 = 55.0%)
func parseDHT22Message(line string) (*DHT22Reading, error) {
	if !strings.HasPrefix(line, "MSG:T:") {
		return nil, fmt.Errorf("not a temperature message")
	}

	// Extract temperature and humidity
	parts := strings.Split(line[6:], "-H:")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid message format")
	}

	tempRaw, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid temperature: %w", err)
	}

	humRaw, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid humidity: %w", err)
	}

	return &DHT22Reading{
		Temperature: tempRaw / 10.0,
		Humidity:    humRaw / 10.0,
	}, nil
}

// calculateFanSpeed determines fan speed based on GPU temperatures
func calculateFanSpeed(gpuTemps []GPUTemperature) int {
	if len(gpuTemps) == 0 {
		return 50 // Default speed
	}

	// Find max GPU temperature
	maxTemp := 0.0
	for _, gpu := range gpuTemps {
		if gpu.Temperature > maxTemp {
			maxTemp = gpu.Temperature
		}
	}

	// Fan curve:
	// < 50°C: 30%
	// 50-60°C: 30-50%
	// 60-70°C: 50-75%
	// 70-80°C: 75-90%
	// > 80°C: 100%

	switch {
	case maxTemp < 50:
		return 30
	case maxTemp < 60:
		return 30 + int((maxTemp-50)*2)
	case maxTemp < 70:
		return 50 + int((maxTemp-60)*2.5)
	case maxTemp < 80:
		return 75 + int((maxTemp-70)*1.5)
	default:
		return 100
	}
}

// setFanSpeed sends fan speed command to Arduino
func setFanSpeed(port *serial.Port, speed int) error {
	if speed < 0 {
		speed = 0
	}
	if speed > 100 {
		speed = 100
	}

	command := fmt.Sprintf("FAN:%d\n", speed)
	_, err := port.Write([]byte(command))
	if err != nil {
		return fmt.Errorf("failed to write to serial: %w", err)
	}

	return nil
}

type AlertState struct {
	GPUWarning      bool
	GPUCritical     bool
	AmbientWarning  bool
	LastFanSpeed    int
	LastAmbientTemp float64
}

func monitorAndControl(config Config, port *serial.Port, scanner *bufio.Scanner, alertState *AlertState) {
	// Read GPU temperatures
	gpuTemps, err := getGPUTemperatures()
	if err != nil {
		log.Printf("Error reading GPU temperatures: %v", err)
	} else {
		// Log GPU temps
		for _, gpu := range gpuTemps {
			log.Printf("GPU %d (%s): %.1f°C", gpu.Index, gpu.Name, gpu.Temperature)
		}

		// Calculate and set fan speed
		fanSpeed := calculateFanSpeed(gpuTemps)
		if fanSpeed != alertState.LastFanSpeed {
			if err := setFanSpeed(port, fanSpeed); err != nil {
				log.Printf("Error setting fan speed: %v", err)
			} else {
				log.Printf("Fan speed set to %d%%", fanSpeed)
				alertState.LastFanSpeed = fanSpeed
			}
		}

		// Check for GPU alerts
		maxTemp := 0.0
		hotGPU := GPUTemperature{}
		for _, gpu := range gpuTemps {
			if gpu.Temperature > maxTemp {
				maxTemp = gpu.Temperature
				hotGPU = gpu
			}
		}

		// Critical threshold
		if maxTemp > config.CriticalThreshold {
			if !alertState.GPUCritical {
				event := apialerts.Event{
					Channel: config.Channel,
					Message: fmt.Sprintf(
						"🔥 CRITICAL: GPU %d (%s) temperature is dangerously high: %.1f°C (threshold: %.1f°C)",
						hotGPU.Index, hotGPU.Name, maxTemp, config.CriticalThreshold,
					),
					Tags: []string{"gpu-monitoring", "temperature-critical", "urgent"},
				}
				if err := apialerts.SendAsync(event); err != nil {
					log.Printf("Failed to send critical alert: %v", err)
				} else {
					alertState.GPUCritical = true
					alertState.GPUWarning = true
					log.Printf("CRITICAL alert sent for GPU %d", hotGPU.Index)
				}
			}
		} else if maxTemp > config.WarningThreshold {
			if !alertState.GPUWarning {
				event := apialerts.Event{
					Channel: config.Channel,
					Message: fmt.Sprintf(
						"⚠️ GPU %d (%s) temperature elevated: %.1f°C (threshold: %.1f°C)",
						hotGPU.Index, hotGPU.Name, maxTemp, config.WarningThreshold,
					),
					Tags: []string{"gpu-monitoring", "temperature-warning"},
				}
				if err := apialerts.SendAsync(event); err != nil {
					log.Printf("Failed to send warning alert: %v", err)
				} else {
					alertState.GPUWarning = true
					log.Printf("Warning alert sent for GPU %d", hotGPU.Index)
				}
			}
			if alertState.GPUCritical {
				alertState.GPUCritical = false
				log.Printf("GPU dropped from critical to warning level")
			}
		} else {
			if alertState.GPUWarning || alertState.GPUCritical {
				event := apialerts.Event{
					Channel: config.Channel,
					Message: fmt.Sprintf("✅ GPU temperatures back to normal: %.1f°C", maxTemp),
					Tags:    []string{"gpu-monitoring", "temperature-recovery"},
				}
				if err := apialerts.SendAsync(event); err != nil {
					log.Printf("Failed to send recovery alert: %v", err)
				} else {
					log.Printf("GPU temperatures back to normal")
				}
				alertState.GPUWarning = false
				alertState.GPUCritical = false
			}
		}
	}

	// Read DHT22 data from Arduino (non-blocking)
	for scanner.Scan() {
		line := scanner.Text()

		if reading, err := parseDHT22Message(line); err == nil {
			log.Printf("Ambient: %.1f°C, Humidity: %.1f%%", reading.Temperature, reading.Humidity)

			// Check ambient temperature threshold
			if reading.Temperature > config.AmbientThreshold {
				if !alertState.AmbientWarning {
					event := apialerts.Event{
						Channel: config.Channel,
						Message: fmt.Sprintf(
							"🌡️ Ambient temperature elevated: %.1f°C (threshold: %.1f°C)",
							reading.Temperature, config.AmbientThreshold,
						),
						Tags: []string{"ambient-monitoring", "temperature-warning"},
					}
					if err := apialerts.SendAsync(event); err != nil {
						log.Printf("Failed to send ambient alert: %v", err)
					} else {
						alertState.AmbientWarning = true
						log.Printf("Ambient temperature alert sent")
					}
				}
			} else {
				if alertState.AmbientWarning {
					event := apialerts.Event{
						Channel: config.Channel,
						Message: fmt.Sprintf("✅ Ambient temperature back to normal: %.1f°C", reading.Temperature),
						Tags:    []string{"ambient-monitoring", "temperature-recovery"},
					}
					if err := apialerts.SendAsync(event); err != nil {
						log.Printf("Failed to send ambient recovery alert: %v", err)
					} else {
						alertState.AmbientWarning = false
						log.Printf("Ambient temperature back to normal")
					}
				}
			}

			alertState.LastAmbientTemp = reading.Temperature
		}

		// Don't block - only read what's available
		break
	}
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("GPU Temperature Fan Controller Service starting...")

	config := loadConfig()
	log.Printf("Configuration: GPU Warning=%.1f°C, Critical=%.1f°C, Ambient=%.1f°C, Interval=%v",
		config.WarningThreshold, config.CriticalThreshold, config.AmbientThreshold, config.CheckInterval)

	// Initialize API Alerts client
	apialerts.Configure(config.APIKey)
	log.Println("API Alerts client configured")

	// Connect to Arduino (auto-detect if port not specified)
	var port *serial.Port
	var detectedPort string
	if config.SerialPort != "" && config.SerialPort != defaultSerialPort {
		// Use specified port
		serialConfig := &serial.Config{
			Name: config.SerialPort,
			Baud: config.SerialBaud,
		}
		var err error
		port, err = serial.OpenPort(serialConfig)
		if err != nil {
			log.Fatalf("Failed to open specified serial port %s: %v", config.SerialPort, err)
		}
		detectedPort = config.SerialPort
		log.Printf("Connected to Arduino on %s", detectedPort)
	} else {
		// Auto-detect Arduino
		var err error
		port, detectedPort, err = detectArduino(config.SerialBaud)
		if err != nil {
			log.Fatalf("Failed to detect Arduino: %v", err)
		}
		log.Printf("Auto-detected Arduino on %s", detectedPort)
	}
	defer port.Close()

	// Wait for Arduino to reset
	time.Sleep(2 * time.Second)

	// Create scanner for reading Arduino messages
	scanner := bufio.NewScanner(port)

	// Send startup notification
	startupEvent := apialerts.Event{
		Channel: config.Channel,
		Message: fmt.Sprintf(
			"GPU Temperature Fan Controller started (GPU warning: %.1f°C, critical: %.1f°C, ambient: %.1f°C)",
			config.WarningThreshold, config.CriticalThreshold, config.AmbientThreshold,
		),
		Tags: []string{"gpu-monitoring", "service-start"},
	}
	if err := apialerts.SendAsync(startupEvent); err != nil {
		log.Printf("Warning: Failed to send startup notification: %v", err)
	}

	// Initialize alert state
	alertState := &AlertState{
		LastFanSpeed: 50, // Match Arduino default
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create ticker for periodic checks
	ticker := time.NewTicker(config.CheckInterval)
	defer ticker.Stop()

	// Initial check
	monitorAndControl(config, port, scanner, alertState)

	// Main monitoring loop
	for {
		select {
		case <-ticker.C:
			monitorAndControl(config, port, scanner, alertState)

		case sig := <-sigChan:
			log.Printf("Received signal %v, shutting down gracefully...", sig)

			// Send shutdown notification
			shutdownEvent := apialerts.Event{
				Channel: config.Channel,
				Message: "GPU Temperature Fan Controller shutting down",
				Tags:    []string{"gpu-monitoring", "service-stop"},
			}
			apialerts.Send(shutdownEvent)

			return
		}
	}
}
