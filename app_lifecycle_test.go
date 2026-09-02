package main

import (
	"context"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/desktop"
)

func TestBeforeCloseLinuxWithoutTrayHidesOnlyAfterUserChooses(t *testing.T) {
	var promptCalls int
	var hidden bool
	app := &App{
		desktopLifecycle: desktop.NewLifecycle(desktop.PlatformLinux, false),
		closePrompt: func(context.Context, desktop.ClosePrompt) (desktop.CloseChoice, error) {
			promptCalls++
			return desktop.CloseChoiceHide, nil
		},
		hideWindow: func(context.Context) { hidden = true },
	}

	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Fatal("beforeClose() = false, want hidden window to prevent exit")
	}
	if promptCalls != 1 {
		t.Fatalf("close prompt calls = %d, want 1", promptCalls)
	}
	if !hidden {
		t.Fatal("window was not hidden after user chose background mode")
	}
}

func TestBeforeCloseLinuxWithoutTrayExitsOnlyAfterUserChoosesExit(t *testing.T) {
	var hidden bool
	app := &App{
		desktopLifecycle: desktop.NewLifecycle(desktop.PlatformLinux, false),
		closePrompt: func(context.Context, desktop.ClosePrompt) (desktop.CloseChoice, error) {
			return desktop.CloseChoiceExit, nil
		},
		hideWindow: func(context.Context) { hidden = true },
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose() = true, want exit after user chose exit")
	}
	if hidden {
		t.Fatal("window was hidden after user chose exit")
	}
	if !app.allowClose.Load() {
		t.Fatal("explicit exit was not recorded")
	}
}

func TestBeforeCloseDarwinAllowsDockQuit(t *testing.T) {
	var hidden bool
	app := &App{
		desktopLifecycle: desktop.NewLifecycle(desktop.PlatformDarwin, true),
		hideWindow:       func(context.Context) { hidden = true },
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose() = true, want Darwin application quit to proceed")
	}
	if hidden {
		t.Fatal("Dock/Cmd+Q must not be turned into a hide")
	}
}

func TestBeforeCloseLinuxWithTrayHidesWithoutPrompt(t *testing.T) {
	var promptCalls int
	var hidden bool
	app := &App{
		desktopLifecycle: desktop.NewLifecycle(desktop.PlatformLinux, true),
		closePrompt: func(context.Context, desktop.ClosePrompt) (desktop.CloseChoice, error) {
			promptCalls++
			return desktop.CloseChoiceExit, nil
		},
		hideWindow: func(context.Context) { hidden = true },
	}

	if prevent := app.beforeClose(context.Background()); !prevent {
		t.Fatal("beforeClose() = false, want tray session to hide")
	}
	if promptCalls != 0 {
		t.Fatalf("close prompt calls = %d, want 0", promptCalls)
	}
	if !hidden {
		t.Fatal("window was not hidden in tray session")
	}
}

func TestCloseChoiceAcceptsLinuxNativeYesNoResponses(t *testing.T) {
	prompt := desktop.ClosePromptForLocale("zh-CN")
	if got, want := closeChoiceFromDialogAnswer(prompt, "Yes"), desktop.CloseChoiceExit; got != want {
		t.Fatalf("Yes choice = %q, want %q", got, want)
	}
	if got, want := closeChoiceFromDialogAnswer(prompt, "No"), desktop.CloseChoiceHide; got != want {
		t.Fatalf("No choice = %q, want %q", got, want)
	}
	if got, want := closeChoiceFromDialogAnswer(prompt, ""), desktop.CloseChoiceCancel; got != want {
		t.Fatalf("empty choice = %q, want %q", got, want)
	}
}
