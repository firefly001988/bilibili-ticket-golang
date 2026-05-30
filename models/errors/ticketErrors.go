package errors

import "fmt"

// ── Mall ticket errors ─────────────────────────────────────────────────────

type TicketEmptyContactError struct {
	ProjectID string
	SkuID     string
	ScreenID  string
}

func NewTicketEmptyContactError(project string, sku string, screen string) *TicketEmptyContactError {
	return &TicketEmptyContactError{
		ProjectID: project,
		SkuID:     sku,
		ScreenID:  screen,
	}
}

func (ta *TicketEmptyContactError) Error() string {
	return fmt.Sprintf("The ticket %s-%s-%s contact is empty", ta.ProjectID, ta.SkuID, ta.ScreenID)
}

type RoutineCreateError struct {
	Message string
}

func (e *RoutineCreateError) Error() string {
	return e.Message
}

func NewRoutineCreateError(message string) error {
	return &RoutineCreateError{
		Message: message,
	}
}

// ── BWS reservation errors ─────────────────────────────────────────────────

// BWSReservationError wraps an error returned by the BWS reservation API.
type BWSReservationError struct {
	ActivityID int
	Code       int
	Message    string
}

func NewBWSReservationError(activityID int, code int, message string) *BWSReservationError {
	return &BWSReservationError{
		ActivityID: activityID,
		Code:       code,
		Message:    message,
	}
}

func (e *BWSReservationError) Error() string {
	return fmt.Sprintf("BWS reservation failed for activity %d: code=%d, message=%s", e.ActivityID, e.Code, e.Message)
}

// BWSTaskCreateError indicates that a BWS task could not be created.
type BWSTaskCreateError struct {
	Message string
}

func NewBWSTaskCreateError(message string) *BWSTaskCreateError {
	return &BWSTaskCreateError{Message: message}
}

func (e *BWSTaskCreateError) Error() string {
	return fmt.Sprintf("BWS task create error: %s", e.Message)
}
