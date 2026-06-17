package i18n

import (
	"embed"
	"encoding/json"
	"sync"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

var (
	bundle        *goi18n.Bundle
	currentLocale string
	mu            sync.RWMutex
	initOnce      sync.Once
)

var supportedLocales []string

// Init initializes the i18n bundle. Safe to call multiple times (no-op after first).
// Locale files are embedded from internal/i18n/locales/*.json.
func init() {
	localeEntries, _ := localeFS.ReadDir("locales")
	for _, entry := range localeEntries {
		if !entry.IsDir() {
			supportedLocales = append(supportedLocales, entry.Name()[:len(entry.Name())-5]) // Remove ".json" extension
		}
	}
	initOnce.Do(func() {
		bundle = goi18n.NewBundle(language.Chinese)
		bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

		entries, err := localeFS.ReadDir("locales")
		if err != nil {
			panic("i18n: failed to read embedded locales directory: " + err.Error())
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			_, loadErr := bundle.LoadMessageFileFS(localeFS, "locales/"+entry.Name())
			if loadErr != nil {
				panic("i18n: failed to load message file " + entry.Name() + ": " + loadErr.Error())
			}
		}
	})
}

// SetLocale sets the current locale (e.g., "zh-CN", "en").
func SetLocale(loc string) {
	mu.Lock()
	defer mu.Unlock()
	for _, locale := range supportedLocales {
		if locale == loc {
			currentLocale = loc
			return
		}
	}
}

// GetLocale returns the current locale string.
// Returns empty string if SetLocale has never been called (first startup).
func GetLocale() string {
	mu.RLock()
	defer mu.RUnlock()
	return currentLocale
}

// T returns the localized string for the given message ID.
// Pass nil for templateData when the message has no template variables.
// Falls back to returning the messageID itself if localization fails.
func T(messageID string, templateData map[string]interface{}) string {
	mu.RLock()
	loc := currentLocale
	mu.RUnlock()

	// Prefer current locale, fall back to zh-CN (source language)
	langs := []string{loc, "zh-CN"}
	localizer := goi18n.NewLocalizer(bundle, langs...)

	result, err := localizer.Localize(&goi18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: templateData,
	})
	if err != nil {
		return messageID
	}
	return result
}
