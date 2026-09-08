package server

import (
	"encoding/json"
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

func TestHandleWindowSidebarSettingsPersistsPreference(t *testing.T) {
	testenv.WingmanHome(t)
	s := &Server{}
	for _, position := range []settings.WindowSidebarPosition{
		settings.WindowSidebarPositionRight,
		settings.WindowSidebarPositionLeft,
	} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/settings/window.sidebar.position",
			strings.NewReader(`{"window.sidebar.position":"`+string(position)+`"}`),
		)
		recorder := httptest.NewRecorder()
		s.handleWindowSidebarSettings(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response map[string]settings.WindowSidebarPosition
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["window.sidebar.position"] != position {
			t.Fatalf("response = %v, want %q", response, position)
		}
		if got := s.windowSidebarPosition(); got != position {
			t.Fatalf("server position = %q, want %q", got, position)
		}
		value, err := settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		if value.WindowSidebarPosition != position {
			t.Fatalf("persisted position = %q, want %q", value.WindowSidebarPosition, position)
		}
	}
}

func TestHandleWindowSidebarSettingsRejectsInvalidPreference(t *testing.T) {
	for _, body := range []string{
		`{"window.sidebar.position":"bottom"}`,
		`{"window.sidebar.position":""}`,
		`{"window.sidebar.position":null}`,
		`{"window.sidebar.position":false}`,
		`{"window.sidebar.position":"left","unknown":true}`,
		`{}`,
		`{`,
	} {
		t.Run(body, func(t *testing.T) {
			testenv.WingmanHome(t)
			if _, err := settings.Update(func(value *settings.Settings) {
				value.WindowSidebarPosition = settings.WindowSidebarPositionRight
			}); err != nil {
				t.Fatal(err)
			}
			s := &Server{}
			s.sidebarPosition.Store(settings.WindowSidebarPositionRight)
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/settings/window.sidebar.position",
				strings.NewReader(body),
			)
			recorder := httptest.NewRecorder()
			s.handleWindowSidebarSettings(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if got := s.windowSidebarPosition(); got != settings.WindowSidebarPositionRight {
				t.Fatalf("server position = %q, want right", got)
			}
			value, err := settings.Load()
			if err != nil {
				t.Fatal(err)
			}
			if value.WindowSidebarPosition != settings.WindowSidebarPositionRight {
				t.Fatalf("persisted position = %q, want right", value.WindowSidebarPosition)
			}
		})
	}
}
