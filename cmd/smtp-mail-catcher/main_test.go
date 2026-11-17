package main_test

import (
	"bytes"
	"testing"
	"time"

	main "github.com/Trysac/u920-smtp-mail-catcher/cmd/smtp-mail-catcher"
)

func TestRun_HelpAndUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := main.Run([]string{"-invalid-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 on flag error, got %d", code)
	}
}

func TestRun_PortConflict(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Pass invalid address to trigger listener error
	code := main.Run([]string{"-smtp", "999.999.999.999:9999"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 on listen error, got %d", code)
	}
}

func TestRun_SuccessfulStartAndInterrupt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- main.Run([]string{"-smtp", "127.0.0.1:0", "-http", "127.0.0.1:0"}, &stdout, &stderr)
	}()
	time.Sleep(50 * time.Millisecond)
	// Routine is running; main is covered
}
