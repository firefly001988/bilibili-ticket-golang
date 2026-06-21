package token

import "time"

// NormalTokenGenerator generates empty tokens for non-hot projects.
type NormalTokenGenerator struct{}

// NewNormalTokenGenerator creates a new NormalTokenGenerator.
func NewNormalTokenGenerator() *NormalTokenGenerator {
	return &NormalTokenGenerator{}
}

func (g *NormalTokenGenerator) GenerateTokenPrepareStage() string {
	return ""
}

func (g *NormalTokenGenerator) GenerateTokenCreateStage(_ time.Time) string {
	return ""
}

func (g *NormalTokenGenerator) IsHotProject() bool {
	return false
}
