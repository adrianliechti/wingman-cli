package code

import (
	"cmp"
	"fmt"
	"slices"

	corecode "github.com/adrianliechti/wingman-agent/pkg/code"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) showSessionPicker(back bool) {
	currentID := a.sessionID
	go func() {
		sessions, err := a.agent.ListSessions(a.ctx)
		a.post(func() {
			if a.popup != nil || a.sessionID != currentID {
				return
			}
			if err != nil {
				a.showToast("Could not list sessions: "+err.Error(), theme.Default.Red)
				if back {
					a.showCommandCenter()
				}
				return
			}

			filtered := sessions[:0]
			for _, session := range sessions {
				if session.ID != currentID {
					filtered = append(filtered, session)
				}
			}
			sessions = filtered
			slices.SortFunc(sessions, func(a, b corecode.SessionInfo) int {
				return cmp.Compare(b.UpdatedAt.UnixNano(), a.UpdatedAt.UnixNano())
			})

			if len(sessions) == 0 {
				a.showToast("No other sessions to resume", theme.Default.Yellow)
				if back {
					a.showCommandCenter()
				}
				return
			}

			items := make([]PopupItem, 0, len(sessions))
			byID := make(map[string]corecode.SessionInfo, len(sessions))
			for _, session := range sessions {
				label := session.Title
				if label == "" {
					label = "Untitled session"
				}
				items = append(items, PopupItem{
					ID: session.ID, Label: label,
					Detail: session.UpdatedAt.Format("Jan 2 15:04") + " · " + shortSessionID(session.ID),
				})
				byID[session.ID] = session
			}

			kind := popupList
			title := "resume session"
			if back {
				kind = popupPalette
				title = "commands › sessions"
			}
			popup := newPopup(kind, title, items, func(ids []string) {
				a.loadSessionInfo(byID[ids[0]])
			})
			if back {
				popup.onCancel = a.showCommandCenter
			}
			a.popup = popup
			a.invalidate()
		})
	}()
}

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (a *App) loadSessionInfo(info corecode.SessionInfo) {
	t := theme.Default
	previousID := a.sessionID
	if err := a.agent.LoadSession(a.ctx, info.ID); err != nil {
		a.showToast(fmt.Sprintf("Failed to load session: %v", err), t.Red)
		return
	}

	a.turns.CancelAll(previousID)
	a.activateSession(info.ID)

	a.showWelcome = false
	a.chat = nil
	a.chatScroll = 0
	a.follow = true
	a.clearSelection()
	a.syncMessages()

	banner := fmt.Sprintf("Resumed session from %s", info.UpdatedAt.Format("Jan 2 15:04"))
	a.appendAnnotation(func(width int) []string {
		return cellNotice(banner, t.Green, width)
	})

	if _, ok := a.agent.(recapProvider); ok && len(a.agent.Messages(a.sessionID)) > 0 {
		a.showRecap()
	}
}
