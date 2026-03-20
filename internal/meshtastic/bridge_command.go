package meshtastic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

type bridgeCommandSession struct {
	stdout io.ReadCloser

	cancel context.CancelFunc
	cmd    *exec.Cmd
	waitCh <-chan error

	once sync.Once
}

func startBridgeCommand(ctx context.Context, cfg config.MeshtasticConfig, device string, logger *slog.Logger) (*bridgeCommandSession, error) {
	command, args, err := resolveBridgeCommand(cfg, device)
	if err != nil {
		return nil, err
	}

	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, command, args...)
	cmd.Env = append(
		os.Environ(),
		"MESHTASTIC_PORT="+device,
		"LORAMAPR_MESHTASTIC_DEVICE="+device,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bridge stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bridge stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start bridge command: %w", err)
	}

	if logger != nil {
		go logBridgeStderr(stderr, logger, command)
	} else {
		go drainReader(stderr)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
		close(waitCh)
	}()

	return &bridgeCommandSession{
		stdout: stdout,
		cancel: cancel,
		cmd:    cmd,
		waitCh: waitCh,
	}, nil
}

func (s *bridgeCommandSession) stop() error {
	var result error
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.stdout != nil {
			_ = s.stdout.Close()
		}
		if s.waitCh != nil {
			result = <-s.waitCh
		}
	})
	return result
}

func resolveBridgeCommand(cfg config.MeshtasticConfig, device string) (string, []string, error) {
	device = strings.TrimSpace(device)
	if device == "" {
		return "", nil, errors.New("bridge command requires non-empty device")
	}

	bridgeCommand := strings.TrimSpace(cfg.BridgeCommand)
	bridgeArgs := append([]string(nil), cfg.BridgeArgs...)
	if bridgeCommand == "" {
		exePath, err := os.Executable()
		if err != nil {
			return "", nil, fmt.Errorf("resolve executable path for default bridge command: %w", err)
		}
		bridgeCommand = exePath
		bridgeArgs = []string{"meshtastic-bridge", "-device", device}
		return bridgeCommand, bridgeArgs, nil
	}

	bridgeCommand = replaceBridgeTokens(bridgeCommand, device)
	for idx := range bridgeArgs {
		bridgeArgs[idx] = replaceBridgeTokens(bridgeArgs[idx], device)
	}
	return bridgeCommand, bridgeArgs, nil
}

func replaceBridgeTokens(value, device string) string {
	out := strings.TrimSpace(value)
	if out == "" {
		return ""
	}
	out = strings.ReplaceAll(out, "{{device}}", device)
	out = strings.ReplaceAll(out, "${MESHTASTIC_PORT}", device)
	out = strings.ReplaceAll(out, "$MESHTASTIC_PORT", device)
	return out
}

func logBridgeStderr(reader io.Reader, logger *slog.Logger, command string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "traceback") || strings.Contains(lower, "panic") {
			logger.Warn("meshtastic bridge stderr", "command", command, "line", line)
		} else {
			logger.Debug("meshtastic bridge stderr", "command", command, "line", line)
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Debug("meshtastic bridge stderr closed", "command", command, "err", err)
	}
}

func drainReader(reader io.Reader) {
	_, _ = io.Copy(io.Discard, reader)
}
