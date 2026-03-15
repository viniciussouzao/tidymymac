package tui

type screen int

const (
	screenDashboard screen = iota
	screenScanning
	screenReview
	screenCleaning
	screenSummary
)
