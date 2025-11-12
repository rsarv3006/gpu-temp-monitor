package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apialerts/apialerts-go"
)

const (
	// Configuration
	defaultWarningThreshold  = 70.0             // Warning threshold in Celsius
	defaultCriticalThreshold = 80.0             // Critical threshold in Celsius
	defaultCheckInterval     = 5                // Check interval in seconds
	defaultChannel           = "gpu-monitoring" // Default API Alerts channel
)

type Config struct {
	APIKey            string
	Channel           string
	WarningThreshold  float64
	CriticalThreshold float64
	CheckInterval     time.Duration
}

type GPUTemperature struct {
	Index       int
	Temperature float64
	Name        string
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

	interval := defaultCheckInterval
	if i := os.Getenv("GPU_CHECK_INTERVAL"); i != "" {
		if parsed, err := strconv.Atoi(i); err == nil {
			interval = parsed
		}
	}

	return Config{
		APIKey:            apiKey,
		Channel:           channel,
		WarningThreshold:  warningThreshold,
		CriticalThreshold: criticalThreshold,
		CheckInterval:     time.Duration(interval) * time.Second,
	}
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

func sendWarningAlert(config Config, gpu GPUTemperature) error {
	event := apialerts.Event{
		Channel: config.Channel,
		Message: fmt.Sprintf(
			"⚠️ GPU %d (%s) temperature elevated: %.1f°C (warning threshold: %.1f°C)",
			gpu.Index, gpu.Name, gpu.Temperature, config.WarningThreshold,
		),
		Tags: []string{
			"gpu-monitoring",
			fmt.Sprintf("gpu-%d", gpu.Index),
			"temperature-warning",
		},
	}

	return apialerts.SendAsync(event)
}

func sendCriticalAlert(config Config, gpu GPUTemperature) error {
	event := apialerts.Event{
		Channel: config.Channel,
		Message: fmt.Sprintf(
			"🔥 CRITICAL: GPU %d (%s) temperature is dangerously high: %.1f°C (critical threshold: %.1f°C) - GPU may be damaged!",
			gpu.Index, gpu.Name, gpu.Temperature, config.CriticalThreshold,
		),
		Tags: []string{
			"gpu-monitoring",
			fmt.Sprintf("gpu-%d", gpu.Index),
			"temperature-critical",
			"urgent",
		},
	}

	return apialerts.SendAsync(event)
}

func sendRecoveryAlert(config Config, gpu GPUTemperature) error {
	event := apialerts.Event{
		Channel: config.Channel,
		Message: fmt.Sprintf(
			"✅ GPU %d (%s) temperature back to normal: %.1f°C",
			gpu.Index, gpu.Name, gpu.Temperature,
		),
		Tags: []string{
			"gpu-monitoring",
			fmt.Sprintf("gpu-%d", gpu.Index),
			"temperature-recovery",
		},
	}

	return apialerts.SendAsync(event)
}

type AlertState struct {
	Warning  bool
	Critical bool
}

func monitorTemperatures(config Config, alertStates map[int]*AlertState) {
	temps, err := getGPUTemperatures()
	if err != nil {
		log.Printf("Error reading GPU temperatures: %v", err)
		return
	}

	for _, gpu := range temps {
		log.Printf("GPU %d (%s): %.1f°C", gpu.Index, gpu.Name, gpu.Temperature)

		// Initialize alert state if it doesn't exist
		if alertStates[gpu.Index] == nil {
			alertStates[gpu.Index] = &AlertState{}
		}
		state := alertStates[gpu.Index]

		// Check critical threshold first
		if gpu.Temperature > config.CriticalThreshold {
			if !state.Critical {
				if err := sendCriticalAlert(config, gpu); err != nil {
					log.Printf("Failed to send critical alert for GPU %d: %v", gpu.Index, err)
				} else {
					state.Critical = true
					state.Warning = true // Mark warning as sent too
					log.Printf("CRITICAL alert sent for GPU %d", gpu.Index)
				}
			}
		} else if gpu.Temperature > config.WarningThreshold {
			// In warning zone but not critical
			if !state.Warning {
				if err := sendWarningAlert(config, gpu); err != nil {
					log.Printf("Failed to send warning alert for GPU %d: %v", gpu.Index, err)
				} else {
					state.Warning = true
					log.Printf("Warning alert sent for GPU %d", gpu.Index)
				}
			}
			// Reset critical flag if we dropped from critical to warning
			if state.Critical {
				state.Critical = false
				log.Printf("GPU %d dropped from critical to warning level", gpu.Index)
			}
		} else {
			// Temperature is back to normal
			if state.Warning || state.Critical {
				if err := sendRecoveryAlert(config, gpu); err != nil {
					log.Printf("Failed to send recovery alert for GPU %d: %v", gpu.Index, err)
				} else {
					log.Printf("GPU %d temperature back to normal", gpu.Index)
				}
				state.Warning = false
				state.Critical = false
			}
		}
	}
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("GPU Temperature Monitor Service starting...")

	config := loadConfig()
	log.Printf("Configuration: Warning=%.1f°C, Critical=%.1f°C, CheckInterval=%v, Channel=%s",
		config.WarningThreshold, config.CriticalThreshold, config.CheckInterval, config.Channel)

	// Initialize API Alerts client
	apialerts.Configure(config.APIKey)
	log.Println("API Alerts client configured")

	// Send startup notification
	startupEvent := apialerts.Event{
		Channel: config.Channel,
		Message: fmt.Sprintf("GPU Temperature Monitor started (warning: %.1f°C, critical: %.1f°C)", config.WarningThreshold, config.CriticalThreshold),
		Tags:    []string{"gpu-monitoring", "service-start"},
	}
	if err := apialerts.SendAsync(startupEvent); err != nil {
		log.Printf("Warning: Failed to send startup notification: %v", err)
	}

	// Track alert states for each GPU
	alertStates := make(map[int]*AlertState)

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create ticker for periodic checks
	ticker := time.NewTicker(config.CheckInterval)
	defer ticker.Stop()

	// Initial check
	monitorTemperatures(config, alertStates)

	// Main monitoring loop
	for {
		select {
		case <-ticker.C:
			monitorTemperatures(config, alertStates)

		case sig := <-sigChan:
			log.Printf("Received signal %v, shutting down gracefully...", sig)

			// Send shutdown notification
			shutdownEvent := apialerts.Event{
				Channel: config.Channel,
				Message: "GPU Temperature Monitor shutting down",
				Tags:    []string{"gpu-monitoring", "service-stop"},
			}
			apialerts.Send(shutdownEvent)

			return
		}
	}
}
