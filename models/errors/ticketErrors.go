package errors

import "fmt"

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
