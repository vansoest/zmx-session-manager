package tui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
	"github.com/mdsakalu/zmx-session-manager/internal/zmx"
)

const (
	listMaxOuterWidth = 56
	logContentHeight  = 4
)

type state int

const (
	stateNormal state = iota
	stateConfirmKill
	stateKilling
	stateFilter
	stateNewSession
)

type sortMode int

const (
	sortByName sortMode = iota
	sortByClients
	sortByPID
	sortByMemory
	sortByUptime
	sortModeCount
)

// AttachMode describes how zsm should launch the selected zmx session.
type AttachMode uint8

const (
	AttachNone AttachMode = iota
	AttachAndReturn
	AttachReplaceProcess
)

// AttachRequest is the action selected when the TUI exits.
type AttachRequest struct {
	Target string
	Host   string // ssh alias for remote sessions; empty = this machine
	Mode   AttachMode
	// NewSession targets are user input and still need zmx's configured prefix.
	NewSession bool
}

func (s sortMode) label() string {
	switch s {
	case sortByName:
		return "name"
	case sortByClients:
		return "clients"
	case sortByPID:
		return "pid"
	case sortByMemory:
		return "memory"
	case sortByUptime:
		return "uptime"
	}
	return ""
}

// Messages

type sessionsMsg struct {
	sessions []Session
	errs     []error
}
type Session = zmx.Session

type previewMsg struct {
	host    string
	name    string
	content string
}

type statusClearMsg struct{}

type killOneResultMsg struct {
	host string
	name string
	err  error
}

type waitCheckMsg struct {
	names   []string
	attempt int
}

type processInfoMsg struct {
	info map[string]zmx.ProcessInfo
}

type allGoneMsg struct{}

// Commands

func fetchSessionsCmd() tea.Msg {
	sessions, errs := zmx.FetchAll()
	return sessionsMsg{sessions: sessions, errs: errs}
}

func fetchProcessInfoCmd(sessions []Session) tea.Cmd {
	return func() tea.Msg {
		return processInfoMsg{info: zmx.FetchProcessInfo(localSessions(sessions))}
	}
}

// localSessions keeps process probing restricted to local rows; ssh'd ps
// output for remote hosts is not worth the cost here.
func localSessions(sessions []Session) []Session {
	var out []Session
	for _, s := range sessions {
		if !s.IsRemote() {
			out = append(out, s)
		}
	}
	return out
}

func fetchPreviewCmd(host string, name string, lines int) tea.Cmd {
	return func() tea.Msg {
		return previewMsg{host: host, name: name, content: zmx.FetchPreviewByName(host, name, lines)}
	}
}

func killOneCmd(host string, name string) tea.Cmd {
	return func() tea.Msg {
		err := zmx.KillSession(zmx.Session{Host: host, Name: name})
		return killOneResultMsg{host: host, name: name, err: err}
	}
}

func waitForGoneCmd(names []string, attempt int) tea.Cmd {
	return func() tea.Msg {
		if attempt >= 20 {
			return allGoneMsg{}
		}
		time.Sleep(200 * time.Millisecond)
		sessions, err := zmx.FetchSessions()
		if err != nil {
			return allGoneMsg{}
		}
		alive := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			alive[s.Name] = true
		}
		for _, name := range names {
			if alive[name] {
				return waitCheckMsg{names: names, attempt: attempt + 1}
			}
		}
		return allGoneMsg{}
	}
}

func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return statusClearMsg{}
	})
}

// Model

type Model struct {
	sessions   []Session
	cursor     int
	listOffset int
	selected   map[string]bool

	filterText string
	sortMode   sortMode
	sortAsc    bool
	attach     AttachRequest

	newSessionName    string
	newSessionDefault string
	sessionNameBase   string

	preview        string
	previewScrollX int
	previewWrap    bool
	state          state
	status         string

	// Kill tracking
	killQueue     []string
	killNow       string
	killDoneNames []string

	// Activity log
	logLines  []string
	logOffset int

	width  int
	height int
	err    error

	visibleCache      []Session
	visibleCacheDirty bool
	visibleMetrics    listMetrics
	allMetrics        listMetrics
	allMetricsDirty   bool
}

type listMetrics struct {
	nameW   int
	pidW    int
	memW    int
	uptimeW int
	clientW int
}

func initialModel() Model {
	return Model{
		selected:          make(map[string]bool),
		sortAsc:           true,
		previewWrap:       true,
		sessionNameBase:   "session",
		visibleCacheDirty: true,
		allMetricsDirty:   true,
	}
}

func NewModel() Model {
	m := initialModel()
	if cwd, err := os.Getwd(); err == nil {
		base := filepath.Base(cwd)
		if base != "" && base != "." && base != string(filepath.Separator) {
			m.sessionNameBase = base
		}
	}
	return m
}

func (m Model) AttachRequest() AttachRequest {
	return m.attach
}

// visibleSessions returns sessions matching the current filter, sorted by sortMode.
func (m *Model) visibleSessions() []Session {
	if !m.visibleCacheDirty {
		return m.visibleCache
	}
	m.visibleCache = m.computeVisibleSessions()
	m.visibleMetrics = computeListMetrics(m.visibleCache)
	m.visibleCacheDirty = false
	return m.visibleCache
}

func (m *Model) markSessionsChanged() {
	m.visibleCacheDirty = true
	m.allMetricsDirty = true
}

func (m *Model) markVisibleChanged() {
	m.visibleCacheDirty = true
}

func (m *Model) allSessionMetrics() listMetrics {
	if m.allMetricsDirty {
		m.allMetrics = computeListMetrics(m.sessions)
		m.allMetricsDirty = false
	}
	return m.allMetrics
}

func (m *Model) computeVisibleSessions() []Session {
	var filtered []Session
	if m.filterText == "" {
		filtered = make([]Session, len(m.sessions))
		copy(filtered, m.sessions)
	} else {
		lower := strings.ToLower(m.filterText)
		for _, s := range m.sessions {
			if strings.Contains(strings.ToLower(s.Name), lower) ||
				strings.Contains(strings.ToLower(s.StartedIn), lower) ||
				strings.Contains(strings.ToLower(s.Host), lower) {
				filtered = append(filtered, s)
			}
		}
	}

	dir := 1
	if !m.sortAsc {
		dir = -1
	}
	switch m.sortMode {
	case sortByName:
		slices.SortFunc(filtered, func(a, b Session) int {
			return dir * cmp.Compare(a.Name, b.Name)
		})
	case sortByClients:
		slices.SortFunc(filtered, func(a, b Session) int {
			if a.Clients != b.Clients {
				return dir * (a.Clients - b.Clients)
			}
			return cmp.Compare(a.Name, b.Name)
		})
	case sortByPID:
		slices.SortFunc(filtered, func(a, b Session) int {
			ai, _ := strconv.Atoi(a.PID)
			bi, _ := strconv.Atoi(b.PID)
			if ai != bi {
				return dir * (ai - bi)
			}
			return cmp.Compare(a.Name, b.Name)
		})
	case sortByMemory:
		slices.SortFunc(filtered, func(a, b Session) int {
			if a.Memory != b.Memory {
				return dir * cmp.Compare(a.Memory, b.Memory)
			}
			return cmp.Compare(a.Name, b.Name)
		})
	case sortByUptime:
		slices.SortFunc(filtered, func(a, b Session) int {
			if a.Uptime != b.Uptime {
				return dir * (a.Uptime - b.Uptime)
			}
			return cmp.Compare(a.Name, b.Name)
		})
	}

	return filtered
}

func computeListMetrics(sessions []Session) listMetrics {
	metrics := listMetrics{
		pidW:    1,
		memW:    1,
		uptimeW: 1,
		clientW: 2,
	}
	for _, s := range sessions {
		if w := runewidth.StringWidth(s.DisplayName()); w > metrics.nameW {
			metrics.nameW = w
		}
		if w := runewidth.StringWidth(s.PID); w > metrics.pidW {
			metrics.pidW = w
		}
		memLabel := "-"
		if s.Memory > 0 {
			memLabel = zmx.FormatBytes(s.Memory)
		}
		if w := runewidth.StringWidth(memLabel); w > metrics.memW {
			metrics.memW = w
		}
		uptimeLabel := "-"
		if s.Uptime > 0 {
			uptimeLabel = zmx.FormatUptime(s.Uptime)
		}
		if w := runewidth.StringWidth(uptimeLabel); w > metrics.uptimeW {
			metrics.uptimeW = w
		}
		clientLabel := fmt.Sprintf("●%d", s.Clients)
		if w := runewidth.StringWidth(clientLabel); w > metrics.clientW {
			metrics.clientW = w
		}
	}
	return metrics
}

func (m *Model) addLog(line string) {
	ts := logDimStyle.Render(time.Now().Format("15:04:05"))
	m.logLines = append(m.logLines, ts+" "+line)
	maxOff := len(m.logLines) - logContentHeight
	if maxOff < 0 {
		maxOff = 0
	}
	m.logOffset = maxOff
}

// clampCursor ensures cursor and listOffset are valid for the visible list.
func (m *Model) clampCursor() {
	visible := m.visibleSessions()
	if m.cursor >= len(visible) {
		m.cursor = max(0, len(visible)-1)
	}
	if m.listOffset > m.cursor {
		m.listOffset = m.cursor
	}
}

func (m Model) Init() tea.Cmd {
	return fetchSessionsCmd
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		visible := m.visibleSessions()
		if m.state != stateKilling && m.cursor < len(visible) {
			return m, m.previewCmd()
		}

	case sessionsMsg:
		for _, e := range msg.errs {
			m.addLog(confirmStyle.Render("  ✗ " + e.Error()))
		}
		if len(msg.sessions) == 0 && len(msg.errs) > 0 {
			m.err = msg.errs[0]
			return m, nil
		}
		m.sessions = msg.sessions
		m.markSessionsChanged()
		live := make(map[string]bool, len(m.sessions))
		for _, s := range m.sessions {
			live[s.Key()] = true
		}
		for key := range m.selected {
			if !live[key] {
				delete(m.selected, key)
			}
		}
		m.clampCursor()
		cmds := []tea.Cmd{fetchProcessInfoCmd(m.sessions)}
		for _, e := range msg.errs {
			m.addLog(helpStyle.Render("  ⋯ " + e.Error()))
		}
		visible := m.visibleSessions()
		if len(visible) > 0 && m.cursor < len(visible) {
			cmds = append(cmds, m.previewCmd())
		} else {
			m.preview = ""
		}
		return m, tea.Batch(cmds...)

	case processInfoMsg:
		updated := false
		for i := range m.sessions {
			if info, ok := msg.info[m.sessions[i].Name]; ok && !m.sessions[i].IsRemote() {
				m.sessions[i].Memory = info.Memory
				m.sessions[i].Uptime = info.Uptime
				updated = true
			}
		}
		if updated {
			m.markSessionsChanged()
		}

	case previewMsg:
		visible := m.visibleSessions()
		if m.cursor < len(visible) && visible[m.cursor].Key() == msg.host+"\x00"+msg.name && !visible[m.cursor].IsCurrent() {
			m.preview = msg.content
		}

	case killOneResultMsg:
		if msg.err != nil {
			m.addLog(confirmStyle.Render("  ✗ " + msg.name))
		} else {
			m.addLog(statusStyle.Render("  ✓ " + msg.name))
			m.killDoneNames = append(m.killDoneNames, msg.host+"\x00"+msg.name)
		}
		m.killNow = ""
		if len(m.killQueue) > 0 {
			next := m.killQueue[0]
			m.killQueue = m.killQueue[1:]
			m.killNow = next
			m.addLog(helpStyle.Render("  ⋯ " + next))
			return m, killQueueCmd(next)
		}
		if len(m.killDoneNames) > 0 {
			m.addLog(logDimStyle.Render("  Waiting for cleanup..."))
			return m, waitForGoneCmd(m.killDoneNames, 0)
		}
		return m, m.finishKill()

	case waitCheckMsg:
		return m, waitForGoneCmd(msg.names, msg.attempt)

	case allGoneMsg:
		return m, m.finishKill()

	case statusClearMsg:
		m.status = ""

	case tea.KeyPressMsg:
		if m.state == stateKilling {
			if isQuit(msg) {
				return m, tea.Quit
			}
			m.handleLogScroll(msg)
			return m, nil
		}
		if m.state == stateFilter {
			return m.handleFilterKey(msg)
		}
		if m.state == stateNewSession {
			return m.handleNewSessionKey(msg)
		}
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) finishKill() tea.Cmd {
	killed := len(m.killDoneNames)
	m.addLog(statusStyle.Render(fmt.Sprintf("  Done. Killed %d session(s).", killed)))
	m.state = stateNormal
	m.selected = make(map[string]bool)
	m.filterText = ""
	m.markVisibleChanged()
	m.cursor = 0
	m.listOffset = 0
	m.killQueue = nil
	m.killDoneNames = nil
	m.killNow = ""
	return tea.Batch(fetchSessionsCmd, clearStatusAfter(3*time.Second))
}

func (m *Model) previewCmd() tea.Cmd {
	m.preview = ""
	visible := m.visibleSessions()
	if m.cursor >= len(visible) {
		return nil
	}
	if visible[m.cursor].IsCurrent() {
		m.preview = "(current session; preview hidden to avoid recursion)"
		return nil
	}
	s := visible[m.cursor]
	return fetchPreviewCmd(s.Host, s.Name, m.mainContentHeight(1))
}

// killTarget returns the full Session under the cursor or in the selection.
// Selection keys are Session.Key() strings ("host\x00name").
func (m *Model) killTargets() []zmx.Session {
	var targets []zmx.Session
	if len(m.selected) > 0 {
		for _, s := range m.sessions {
			if m.selected[s.Key()] {
				targets = append(targets, s)
			}
		}
		return targets
	}
	visible := m.visibleSessions()
	if m.cursor < len(visible) {
		return []zmx.Session{visible[m.cursor]}
	}
	return nil
}

// killQueueHost/Name decode "host\x00name" queue labels for dispatching.
func killQueueHost(key string) string {
	host, _, _ := strings.Cut(key, "\x00")
	return host
}

func killQueueName(key string) string {
	_, name, _ := strings.Cut(key, "\x00")
	return name
}

func killQueueCmd(key string) tea.Cmd {
	return killOneCmd(killQueueHost(key), killQueueName(key))
}
