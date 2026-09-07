package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/settings"
)

func TestHandleWindowTerminalSettingsPersistsPreference(t *testing.T) {
	testenv.WingmanHome(t)
	s := &Server{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/settings/window.terminal.position",
		strings.NewReader(`{"window.terminal.position":"bottom"}`),
	)
	recorder := httptest.NewRecorder()
	s.handleWindowTerminalSettings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := s.windowTerminalPosition(); got != settings.WindowTerminalPositionBottom {
		t.Fatalf("server position = %q, want bottom", got)
	}
	value, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if value.WindowTerminalPosition != settings.WindowTerminalPositionBottom {
		t.Fatalf("persisted position = %q, want bottom", value.WindowTerminalPosition)
	}
}

func TestHandleWindowTerminalSettingsRejectsUnknownPosition(t *testing.T) {
	testenv.WingmanHome(t)
	s := &Server{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/settings/window.terminal.position",
		strings.NewReader(`{"window.terminal.position":"side"}`),
	)
	recorder := httptest.NewRecorder()
	s.handleWindowTerminalSettings(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := s.windowTerminalPosition(); got != settings.WindowTerminalPositionTab {
		t.Fatalf("server position = %q, want tab", got)
	}
}
