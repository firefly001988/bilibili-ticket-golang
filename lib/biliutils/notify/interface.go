package notify

// Notifier is the interface for sending notification messages.
type Notifier interface {
	Notify(message string) (bool, string) // returns success and optional error message
	Test() (bool, string)                 // returns success and optional error message
}
