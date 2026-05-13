package tui

import (
	"hash/fnv"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/fatih/color"
	"github.com/muesli/termenv"
)

var (
	ColorPrimary   = lipgloss.Color("#7C3AED")
	ColorSecondary = lipgloss.Color("#06B6D4")
	ColorSuccess   = lipgloss.Color("#10B981")
	ColorWarning   = lipgloss.Color("#F59E0B")
	ColorError     = lipgloss.Color("#EF4444")
	ColorMuted     = lipgloss.Color("#6B7280")
	ColorText      = lipgloss.Color("#E5E7EB")
	ColorSubtle    = lipgloss.Color("#374151")
	ColorBg        = lipgloss.Color("#111827")
)

var styles = struct {
	Header     lipgloss.Style
	StatusBar  lipgloss.Style
	LogInfo    lipgloss.Style
	LogWarn    lipgloss.Style
	LogError   lipgloss.Style
	LogDebug   lipgloss.Style
	LogForward lipgloss.Style
	LogTrace   lipgloss.Style
	Service    lipgloss.Style
	Spinner    lipgloss.Style
	Muted      lipgloss.Style
	Bold       lipgloss.Style
	Viewport   lipgloss.Style
}{
	Header:     lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary),
	StatusBar:  lipgloss.NewStyle().Foreground(ColorText).Background(ColorSubtle).Padding(0, 1),
	LogInfo:    lipgloss.NewStyle().Foreground(ColorSecondary),
	LogWarn:    lipgloss.NewStyle().Foreground(ColorWarning),
	LogError:   lipgloss.NewStyle().Bold(true).Foreground(ColorError),
	LogDebug:   lipgloss.NewStyle().Foreground(ColorMuted),
	LogForward: lipgloss.NewStyle().Foreground(ColorText),
	LogTrace:   lipgloss.NewStyle().Foreground(ColorMuted).Italic(true),
	Service:    lipgloss.NewStyle().Bold(true).Foreground(ColorSuccess),
	Spinner:    lipgloss.NewStyle().Foreground(ColorPrimary),
	Muted:      lipgloss.NewStyle().Foreground(ColorMuted),
	Bold:       lipgloss.NewStyle().Bold(true).Foreground(ColorText),
	Viewport:   lipgloss.NewStyle(),
}

// init runs configureColorProfile at package load. This mutates
// PROCESS-WIDE state (lipgloss.SetColorProfile, color.NoColor) so
// every test that imports core/tui inherits the codefly color
// settings — there is no way to import the package without the
// side effect. Tests that need a different profile must call
// ConfigureColorProfile (or set CODEFLY_COLOR before the package
// loads) and restore afterwards; see tui_test.go for the pattern.
func init() {
	configureColorProfile()
}

// ConfigureColorProfile re-applies the CODEFLY_COLOR-driven choice
// after callers have mutated the env. Exported so tests can reset
// global state mid-binary without reimporting the package.
func ConfigureColorProfile() {
	configureColorProfile()
}

func configureColorProfile() {
	switch strings.ToLower(os.Getenv("CODEFLY_COLOR")) {
	case "never", "off", "false", "0":
		lipgloss.SetColorProfile(termenv.Ascii)
		color.NoColor = true
	case "auto":
		// Preserve terminal/env auto-detection for callers that explicitly
		// request it. This includes honoring NO_COLOR.
		profile := termenv.NewOutput(os.Stdout).EnvColorProfile()
		lipgloss.SetColorProfile(profile)
		color.NoColor = profile == termenv.Ascii
	default:
		// Codefly has historically emitted colored CLI output via golor even
		// when the terminal supports it. Force the Lip Gloss renderer to the
		// same high-color default so service logs, status lines, and prompt
		// chrome do not fall back to nearly plain text.
		lipgloss.SetColorProfile(termenv.TrueColor)
		color.NoColor = false
	}
}

// Styles returns the shared style set.
func Styles() *struct {
	Header     lipgloss.Style
	StatusBar  lipgloss.Style
	LogInfo    lipgloss.Style
	LogWarn    lipgloss.Style
	LogError   lipgloss.Style
	LogDebug   lipgloss.Style
	LogForward lipgloss.Style
	LogTrace   lipgloss.Style
	Service    lipgloss.Style
	Spinner    lipgloss.Style
	Muted      lipgloss.Style
	Bold       lipgloss.Style
	Viewport   lipgloss.Style
} {
	return &styles
}

// --- Render helpers: hide lipgloss from callers ---

// RenderHeader renders text as a level-1 or level-2 header.
func RenderHeader(level int, text string) string {
	switch level {
	case 1:
		return lipgloss.NewStyle().Foreground(ColorText).Render(text)
	case 2:
		return styles.Header.Render(text)
	default:
		return text
	}
}

// RenderWarning renders text in the warning style with an icon.
func RenderWarning(text string) string {
	return styles.LogWarn.Render("⚠️ " + text)
}

// RenderTrace renders text in the trace style.
func RenderTrace(text string) string {
	return styles.LogTrace.Render(text)
}

// RenderDebug renders text in the debug style.
func RenderDebug(text string) string {
	return styles.LogDebug.Render(text)
}

// RenderInfo renders text in the info style.
func RenderInfo(text string) string {
	return styles.LogInfo.Render(text)
}

// RenderError renders text in the error style with an icon.
func RenderError(text string) string {
	return styles.LogError.Render("☠️ " + text)
}

// RenderErrorDetail renders text in the error style without an icon.
func RenderErrorDetail(text string) string {
	return styles.LogError.Render(text)
}

// RenderFocus renders text with a rounded border for emphasis.
func RenderFocus(text string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorError).
		Border(lipgloss.RoundedBorder()).
		Render(text)
}

// RenderWithMargin renders text with a standard display margin.
func RenderWithMargin(text string) string {
	return lipgloss.NewStyle().Margin(1, 2, 1, 2).Render(text)
}

// RenderMarkdown renders markdown content with the given glamour style
// (e.g. "dark", "light", "auto").
func RenderMarkdown(content string, style string) (string, error) {
	return glamour.Render(content, style)
}

// --- Service color picker: deterministic per-service styling ---

var serviceColors = []lipgloss.Color{
	lipgloss.Color("#22D3EE"), lipgloss.Color("#A78BFA"),
	lipgloss.Color("#34D399"), lipgloss.Color("#F472B6"),
	lipgloss.Color("#FBBF24"), lipgloss.Color("#60A5FA"),
	lipgloss.Color("#FB7185"), lipgloss.Color("#2DD4BF"),
	lipgloss.Color("#F97316"), lipgloss.Color("#C084FC"),
	lipgloss.Color("#4ADE80"), lipgloss.Color("#38BDF8"),
	lipgloss.Color("#E879F9"), lipgloss.Color("#FACC15"),
	lipgloss.Color("#818CF8"), lipgloss.Color("#14B8A6"),
}

var (
	serviceStyleCache = map[string]func(string) string{}
	serviceStyleMu    sync.RWMutex
)

func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// ServiceRenderer returns a render function that applies a deterministic
// color to text based on the service's unique identifier. Results are cached.
func ServiceRenderer(unique string) func(string) string {
	serviceStyleMu.RLock()
	if fn, ok := serviceStyleCache[unique]; ok {
		serviceStyleMu.RUnlock()
		return fn
	}
	serviceStyleMu.RUnlock()

	parts := strings.Split(unique, "/")
	var style lipgloss.Style
	if len(parts) < 2 {
		style = lipgloss.NewStyle()
	} else {
		hash := hashString(unique)
		fg := serviceColors[hash%uint32(len(serviceColors))]
		style = lipgloss.NewStyle().Foreground(fg)
	}

	fn := func(s string) string { return style.Render(s) }

	serviceStyleMu.Lock()
	serviceStyleCache[unique] = fn
	serviceStyleMu.Unlock()

	return fn
}

// ServiceFocusRenderer returns a render function like ServiceRenderer but
// with a rounded border added, for focus-level log messages.
func ServiceFocusRenderer(unique string) func(string) string {
	parts := strings.Split(unique, "/")
	var style lipgloss.Style
	if len(parts) < 2 {
		style = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	} else {
		hash := hashString(unique)
		fg := serviceColors[hash%uint32(len(serviceColors))]
		style = lipgloss.NewStyle().Foreground(fg).Border(lipgloss.RoundedBorder())
	}
	return func(s string) string { return style.Render(s) }
}
