package handlers

// flash builds the toast payload consumed by the layout's "feedback-toasts"
// partial. kind selects the Bootstrap header class and the auto-hide delay
// ("success" = 4 s, anything else = "danger"/6 s); message is the exact
// user-facing copy rendered inside the toast body.
//
// Handlers set it as data["Flash"] when a server-side event needs to be
// reported on the rendered page (form re-renders, ?saved=1 redirects).
func flash(kind, message string) map[string]any {
	return map[string]any{"Kind": kind, "Message": message}
}
