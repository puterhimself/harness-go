package model

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/charmbracelet/x/ansi"
)

func (m *UI) runtimeClient() (workspace.RLMRuntime, bool) {
	runtime, ok := m.com.Workspace.(workspace.RLMRuntime)
	return runtime, ok
}

func (m *UI) runtimeActionsVisible() bool {
	if m.rlmInspection == nil || m.state != uiChat {
		return false
	}
	if m.focus == uiFocusEditor {
		return false
	}
	if m.isCompact {
		return m.detailsOpen
	}
	return true
}

func (m *UI) refreshRLMInspection(journalLimit int) tea.Cmd {
	if !m.hasSession() {
		return nil
	}
	if m.rlmUnavailable {
		return nil
	}
	runtime, ok := m.runtimeClient()
	if !ok {
		m.rlmInspection = nil
		m.rlmUnavailable = true
		return nil
	}
	sessionID := m.session.ID
	if journalLimit <= 0 {
		journalLimit = 20
	}
	return func() tea.Msg {
		inspection, err := runtime.InspectRuntime(context.Background(), sessionID, journalLimit)
		return rlmInspectionMsg{inspection: inspection, err: err}
	}
}

func (m *UI) applyRLMInspection(inspection workspace.RLMRuntimeInspection) {
	cloned := inspection.Clone()
	m.rlmInspection = &cloned
	m.rlmUnavailable = false
	m.clampRLMBranchSelection()
}

func (m *UI) clampRLMBranchSelection() {
	branches := m.sortedRLMBranches()
	if len(branches) == 0 {
		m.rlmSelectedBranch = 0
		return
	}
	if m.rlmSelectedBranch < 0 {
		m.rlmSelectedBranch = 0
	}
	if m.rlmSelectedBranch >= len(branches) {
		m.rlmSelectedBranch = len(branches) - 1
	}
}

func (m *UI) sortedRLMBranches() []workspace.RLMRuntimeBranch {
	if m.rlmInspection == nil {
		return nil
	}
	branches := append([]workspace.RLMRuntimeBranch(nil), m.rlmInspection.Branches...)
	activeID := m.rlmInspection.ActiveBranchID
	slices.SortFunc(branches, func(a, b workspace.RLMRuntimeBranch) int {
		aActive := a.BranchID == activeID
		bActive := b.BranchID == activeID
		switch {
		case aActive && !bActive:
			return -1
		case !aActive && bActive:
			return 1
		}
		if a.BranchID < b.BranchID {
			return -1
		}
		if a.BranchID > b.BranchID {
			return 1
		}
		return 0
	})
	return branches
}

func (m *UI) selectedRLMBranch() (workspace.RLMRuntimeBranch, bool) {
	branches := m.sortedRLMBranches()
	if len(branches) == 0 {
		return workspace.RLMRuntimeBranch{}, false
	}
	m.clampRLMBranchSelection()
	return branches[m.rlmSelectedBranch], true
}

func (m *UI) moveRLMBranchSelection(delta int) {
	branches := m.sortedRLMBranches()
	if len(branches) == 0 {
		return
	}
	m.clampRLMBranchSelection()
	m.rlmSelectedBranch += delta
	if m.rlmSelectedBranch < 0 {
		m.rlmSelectedBranch = len(branches) - 1
	}
	if m.rlmSelectedBranch >= len(branches) {
		m.rlmSelectedBranch = 0
	}
}

func (m *UI) forkRuntimeBranch() tea.Cmd {
	if !m.hasSession() {
		return nil
	}
	runtime, ok := m.runtimeClient()
	if !ok {
		return nil
	}
	sessionID := m.session.ID
	name := "branch-" + time.Now().UTC().Format("150405")
	return func() tea.Msg {
		branch, err := runtime.ForkRuntimeBranch(context.Background(), sessionID, name)
		if err != nil {
			return rlmBranchActionMsg{action: "fork", err: err}
		}
		return rlmBranchActionMsg{action: "fork", branch: branch.BranchID}
	}
}

func (m *UI) switchSelectedRuntimeBranch() tea.Cmd {
	if !m.hasSession() {
		return nil
	}
	runtime, ok := m.runtimeClient()
	if !ok {
		return nil
	}
	branch, ok := m.selectedRLMBranch()
	if !ok {
		return nil
	}
	sessionID := m.session.ID
	return func() tea.Msg {
		err := runtime.SwitchRuntimeBranch(context.Background(), sessionID, branch.BranchID)
		return rlmBranchActionMsg{action: "switch", branch: branch.BranchID, err: err}
	}
}

func (m *UI) resumeSelectedRuntimeBranch() tea.Cmd {
	if !m.hasSession() {
		return nil
	}
	runtime, ok := m.runtimeClient()
	if !ok {
		return nil
	}
	branch, ok := m.selectedRLMBranch()
	if !ok {
		return nil
	}
	sessionID := m.session.ID
	return func() tea.Msg {
		err := runtime.ResumeRuntimeBranch(context.Background(), sessionID, branch.BranchID)
		return rlmBranchActionMsg{action: "resume", branch: branch.BranchID, err: err}
	}
}

func (m *UI) runtimeSummaryInfo(width int, isSection bool) string {
	if m.rlmInspection == nil {
		return ""
	}
	t := m.com.Styles
	title := t.Subtle.Render("Runtime")
	if isSection {
		title = common.Section(t, "Runtime", width)
	}

	inspection := m.rlmInspection
	state := "idle"
	if m.hasSession() && m.com.Workspace.AgentIsSessionBusy(m.session.ID) {
		state = "running"
	}
	lines := []string{
		fmt.Sprintf("%s %s", t.Subtle.Render("Active:"), t.Base.Render(shortID(inspection.ActiveBranchID))),
		fmt.Sprintf("%s %d", t.Subtle.Render("Branches:"), len(inspection.Branches)),
		fmt.Sprintf("%s %d", t.Subtle.Render("Artifacts:"), inspection.ArtifactCount),
		fmt.Sprintf("%s %d", t.Subtle.Render("Published:"), len(inspection.Published)),
		fmt.Sprintf("%s %d/%d", t.Subtle.Render("Workers:"), inspection.WorkerUsage, inspection.WorkerLimit),
		fmt.Sprintf("%s %s", t.Subtle.Render("State:"), state),
	}
	if inspection.RecoveredFromDisk {
		lines = append(lines, fmt.Sprintf("%s %s", t.Subtle.Render("Recovery:"), t.Base.Render("rehydrated")))
	}

	if inspection.TerminationCause != "" {
		cause := ansi.Truncate(inspection.TerminationCause, max(1, width-8), "...")
		lines = append(lines, fmt.Sprintf("%s %s", t.Subtle.Render("Last:"), cause))
	}
	if m.runtimeActionsVisible() {
		hint := ansi.Truncate("[ ] select, enter switch, r resume, ctrl+b fork", max(1, width), "...")
		lines = append(lines, t.Subtle.Render(hint))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

func (m *UI) runtimeBranchesInfo(width, maxItems int, isSection bool) string {
	if m.rlmInspection == nil {
		return ""
	}
	t := m.com.Styles
	title := t.Subtle.Render("Branches")
	if isSection {
		title = common.Section(t, "Branches", width)
	}
	branches := m.sortedRLMBranches()
	if len(branches) == 0 {
		return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, t.Subtle.Render("None")))
	}

	if maxItems <= 0 {
		maxItems = len(branches)
	}

	m.clampRLMBranchSelection()
	lines := make([]string, 0, min(len(branches), maxItems))
	for i, branch := range branches {
		if i >= maxItems {
			break
		}
		selected := i == m.rlmSelectedBranch
		active := branch.BranchID == m.rlmInspection.ActiveBranchID
		prefix := "  "
		switch {
		case selected:
			prefix = t.Base.Render("> ")
		case active:
			prefix = t.Subtle.Render("* ")
		}
		parent := ""
		if branch.ParentBranchID != "" {
			parent = " <- " + shortID(branch.ParentBranchID)
		}
		descriptor := shortID(branch.BranchID) + parent
		if branch.Status != "" {
			descriptor += " (" + branch.Status + ")"
		}
		if branch.Summary != "" {
			descriptor += " " + branch.Summary
		}
		descriptor = ansi.Truncate(descriptor, max(1, width-2), "...")
		lines = append(lines, prefix+descriptor)
	}
	if len(branches) > maxItems {
		lines = append(lines, t.Subtle.Render(fmt.Sprintf("...and %d more", len(branches)-maxItems)))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

func (m *UI) runtimeTraceInfo(width, maxItems int, isSection bool) string {
	if m.rlmInspection == nil {
		return ""
	}
	t := m.com.Styles
	title := t.Subtle.Render("Trace")
	if isSection {
		title = common.Section(t, "Trace", width)
	}
	entries := m.rlmInspection.RecentJournal
	if len(entries) == 0 {
		return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, t.Subtle.Render("No events")))
	}
	if maxItems <= 0 {
		maxItems = len(entries)
	}

	lines := make([]string, 0, min(maxItems, len(entries)))
	for i := len(entries) - 1; i >= 0 && len(lines) < maxItems; i-- {
		entry := entries[i]
		timeLabel := entry.Timestamp.Local().Format("15:04:05")
		detail := formatRuntimeJournalEntry(entry)
		line := fmt.Sprintf("%s %s", t.Subtle.Render(timeLabel), detail)
		line = ansi.Truncate(line, max(1, width), "...")
		lines = append(lines, line)
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

func formatRuntimeJournalEntry(entry workspace.RLMRuntimeJournalEntry) string {
	kind := entry.Kind
	tool := asString(entry.Payload["tool"])
	summary := asString(entry.Payload["summary"])
	errorText := asString(entry.Payload["error"])
	key := asString(entry.Payload["key"])
	branchID := asString(entry.Payload["active_branch_id"])
	artifactName := asString(entry.Payload["name"])

	switch kind {
	case "tool_call":
		if tool != "" {
			return "tool call: " + tool
		}
		return "tool call"
	case "tool_result":
		if errorText != "" {
			if tool != "" {
				return "tool error: " + tool + " - " + errorText
			}
			return "tool error: " + errorText
		}
		if tool != "" {
			return "tool result: " + tool
		}
		return "tool result"
	case "state_update":
		if key != "" {
			return "state update: " + key
		}
		return "state update"
	case "artifact_put":
		if artifactName != "" {
			return "artifact: " + artifactName
		}
		return "artifact stored"
	case "branch_fork":
		if summary != "" {
			return "forked branch: " + summary
		}
		return "forked branch"
	case "branch_switch":
		if branchID != "" {
			return "switched branch: " + shortID(branchID)
		}
		return "switched branch"
	case "publish":
		if summary != "" {
			return "publish: " + summary
		}
		return "publish"
	case "commit":
		if summary != "" {
			return "commit: " + summary
		}
		return "commit"
	case "recovery_rehydrated":
		return "runtime rehydrated"
	default:
		if errorText != "" {
			return kind + ": " + errorText
		}
		if summary != "" {
			return kind + ": " + summary
		}
		return strings.ReplaceAll(kind, "_", " ")
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func (m *UI) handleRLMActionResult(msg rlmBranchActionMsg) tea.Cmd {
	if msg.err != nil {
		if workspace.IsRLMRuntimeUnavailable(msg.err) {
			m.rlmInspection = nil
			m.rlmUnavailable = true
			return nil
		}
		return util.ReportError(msg.err)
	}
	status := "Runtime updated"
	switch msg.action {
	case "fork":
		status = "Forked branch " + shortID(msg.branch)
	case "switch":
		status = "Switched to branch " + shortID(msg.branch)
	case "resume":
		status = "Resumed branch " + shortID(msg.branch)
	}
	return tea.Batch(util.ReportInfo(status), m.refreshRLMInspection(40))
}
