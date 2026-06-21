package token

import "time"

// Generator defines the token generation strategy for Bilibili ticket ordering.
type Generator interface {
	GenerateTokenPrepareStage() string
	GenerateTokenCreateStage(whenGenPToken time.Time) string
	IsHotProject() bool
}
