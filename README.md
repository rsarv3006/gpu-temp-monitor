# GPU Temperature Monitor Service

A Linux systemd service written in Go that monitors NVIDIA GPU temperatures and sends alerts via [API Alerts](https://apialerts.com) when temperature thresholds are exceeded.

## Features

- 🌡️ **Two-tier alert system**: Warning and Critical temperature thresholds
- 🔔 **Real-time notifications** via API Alerts
- 📊 **Multi-GPU support**: Monitors all NVIDIA GPUs in your system
- 🔄 **Smart state management**: Avoids alert spam with proper debouncing
- ✅ **Recovery notifications**: Alerts when temperatures return to normal
- 🚀 **Automatic restart**: Systemd ensures the service stays running
- 📝 **Full logging**: All events logged via journald

## How It Works

The service continuously monitors your GPU temperatures using `nvidia-smi` and sends alerts through API Alerts:

1. **Warning Level** (default 70°C): "⚠️ Hey, just FYI your GPU is at 72°C"
2. **Critical Level** (default 80°C): "🔥 CRITICAL: GPU at 82°C - calm down or you're gonna cook it!"
3. **Recovery**: "✅ GPU temperature back to normal: 65°C"

## Prerequisites

- Linux system with systemd
- NVIDIA GPU(s) with `nvidia-smi` installed
- Go 1.16 or later
- [API Alerts](https://apialerts.com) account and API key

## Installation

### 1. Clone or Download the Code

Save the Go code to `main.go` and the systemd service file to `gpu-temp-monitor.service`.

### 2. Install Dependencies

```bash
go get github.com/apialerts/apialerts-go
```

### 3. Build the Binary

```bash
go build -o gpu-temp-monitor main.go
```

### 4. Install the Binary

```bash
sudo cp gpu-temp-monitor /usr/local/bin/
sudo chmod +x /usr/local/bin/gpu-temp-monitor
```

### 5. Configure Environment Variables

Create a configuration directory and environment file:

```bash
sudo mkdir -p /etc/gpu-temp-monitor
sudo nano /etc/gpu-temp-monitor/env
```

Add the following content (replace with your actual API key):

```bash
APIALERTS_API_KEY=your-api-key-here
APIALERTS_CHANNEL=gpu-monitoring
GPU_TEMP_WARNING=70
GPU_TEMP_CRITICAL=80
GPU_CHECK_INTERVAL=5
```

Save and exit (Ctrl+X, then Y, then Enter).

### 6. Install the Systemd Service

Copy the service file:

```bash
sudo cp gpu-temp-monitor.service /etc/systemd/system/
```

**Important**: Edit the service file to use the environment file:

```bash
sudo nano /etc/systemd/system/gpu-temp-monitor.service
```

Make sure the `EnvironmentFile` line is uncommented:

```ini
EnvironmentFile=/etc/gpu-temp-monitor/env
```

And comment out or remove the individual `Environment=` lines.

### 7. Enable and Start the Service

```bash
sudo systemctl daemon-reload
sudo systemctl enable gpu-temp-monitor
sudo systemctl start gpu-temp-monitor
```

## Verify Installation

### Check Service Status

```bash
sudo systemctl status gpu-temp-monitor
```

You should see:

```
● gpu-temp-monitor.service - GPU Temperature Monitor Service
   Loaded: loaded (/etc/systemd/system/gpu-temp-monitor.service; enabled)
   Active: active (running) since...
```

### View Live Logs

```bash
sudo journalctl -u gpu-temp-monitor -f
```

You should see temperature readings every 5 seconds (or your configured interval):

```
GPU 0 (NVIDIA GeForce RTX 3080): 65.0°C
GPU 1 (NVIDIA GeForce RTX 3090): 62.0°C
```

### Test Alerts

You should receive a startup notification in your API Alerts channel when the service starts. To test the alert system, you can temporarily lower the thresholds:

```bash
sudo systemctl stop gpu-temp-monitor
sudo nano /etc/gpu-temp-monitor/env
# Set GPU_TEMP_WARNING=50 and GPU_TEMP_CRITICAL=60
sudo systemctl start gpu-temp-monitor
```

## Configuration

### Environment Variables

| Variable             | Description                   | Default          |
| -------------------- | ----------------------------- | ---------------- |
| `APIALERTS_API_KEY`  | Your API Alerts API key       | **Required**     |
| `APIALERTS_CHANNEL`  | API Alerts channel name       | `gpu-monitoring` |
| `GPU_TEMP_WARNING`   | Warning threshold in Celsius  | `70`             |
| `GPU_TEMP_CRITICAL`  | Critical threshold in Celsius | `80`             |
| `GPU_CHECK_INTERVAL` | Check interval in seconds     | `5`              |

### Adjusting Thresholds

Different GPUs have different safe operating temperatures. Common recommendations:

- **Gaming GPUs (RTX 30/40 series)**: Warning 75°C, Critical 85°C
- **Professional GPUs (A100, H100)**: Warning 70°C, Critical 80°C
- **Older GPUs**: Warning 65°C, Critical 75°C

To update configuration:

```bash
sudo nano /etc/gpu-temp-monitor/env
# Make your changes
sudo systemctl restart gpu-temp-monitor
```

## Management Commands

### Start the service

```bash
sudo systemctl start gpu-temp-monitor
```

### Stop the service

```bash
sudo systemctl stop gpu-temp-monitor
```

### Restart the service

```bash
sudo systemctl restart gpu-temp-monitor
```

### Disable auto-start on boot

```bash
sudo systemctl disable gpu-temp-monitor
```

### View recent logs

```bash
sudo journalctl -u gpu-temp-monitor -n 50
```

### View logs from today

```bash
sudo journalctl -u gpu-temp-monitor --since today
```

## Troubleshooting

### Service Won't Start

Check the logs:

```bash
sudo journalctl -u gpu-temp-monitor -n 50
```

Common issues:

- **Missing API key**: Ensure `APIALERTS_API_KEY` is set in `/etc/gpu-temp-monitor/env`
- **nvidia-smi not found**: Install NVIDIA drivers
- **Permission denied**: Ensure binary is executable and owned by root

### Not Receiving Alerts

1. Verify API Alerts configuration:

   ```bash
   grep APIALERTS /etc/gpu-temp-monitor/env
   ```

2. Check if alerts are being sent in logs:

   ```bash
   sudo journalctl -u gpu-temp-monitor | grep "Alert sent"
   ```

3. Test your API key manually with a simple Go script or curl

### High CPU Usage

If the service uses too much CPU, increase the check interval:

```bash
sudo nano /etc/gpu-temp-monitor/env
# Change GPU_CHECK_INTERVAL=5 to GPU_CHECK_INTERVAL=10 or higher
sudo systemctl restart gpu-temp-monitor
```

## Uninstallation

```bash
sudo systemctl stop gpu-temp-monitor
sudo systemctl disable gpu-temp-monitor
sudo rm /etc/systemd/system/gpu-temp-monitor.service
sudo rm /usr/local/bin/gpu-temp-monitor
sudo rm -rf /etc/gpu-temp-monitor
sudo systemctl daemon-reload
```

## API Alerts Integration

This service uses [API Alerts](https://apialerts.com) for notifications. Alerts include:

- **Tags**: `gpu-monitoring`, `gpu-N`, `temperature-warning`, `temperature-critical`, `urgent`
- **Smart routing**: Use API Alerts rules to route critical alerts to different channels (e.g., PagerDuty, Slack, SMS)

Example alert messages:

- ⚠️ Warning: `GPU 0 (NVIDIA GeForce RTX 3080) temperature elevated: 72.0°C`
- 🔥 Critical: `CRITICAL: GPU 0 temperature is dangerously high: 85.0°C - GPU may be damaged!`
- ✅ Recovery: `GPU 0 temperature back to normal: 65.0°C`

## Contributing

Feel free to submit issues or pull requests for improvements!

## License

MIT License - feel free to use and modify as needed.
