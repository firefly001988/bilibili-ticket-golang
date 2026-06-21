package notify

import "bilibili-ticket-golang/cmd/gui/i18n"

// NotificationType classifies notification backends.
type NotificationType int

const (
	None NotificationType = iota
	Gotify
	PushPlus
	Bark
	Ntfy
)

// ConvertNotificationType converts a string name to a NotificationType.
func ConvertNotificationType(name string) NotificationType {
	switch name {
	case "gotify":
		return Gotify
	case "pushplus":
		return PushPlus
	case "bark", "Bark":
		return Bark
	case "ntfy":
		return Ntfy
	default:
		return None
	}
}

// ── Form field metadata (drives the frontend notify form) ──────────────

// SelectOption defines an option item in a select/dropdown field.
type SelectOption struct {
	Label string `json:"label"` // display text, e.g. "Access Token"
	Value string `json:"value"` // option value, e.g. "token"
}

// FieldCondition defines when a field should be visible (depends on another field's value).
type FieldCondition struct {
	Key   string `json:"key"`   // the field key to watch
	Value string `json:"value"` // show this field when the watched field equals this value
}

// NotifyChannelFieldMeta describes a single form field for a notify channel type.
type NotifyChannelFieldMeta struct {
	Key         string          `json:"key"`         // params key, e.g. "endpoint", "token"
	Label       string          `json:"label"`       // display label, e.g. "服务器地址"
	Type        string          `json:"type"`        // input type: "text", "password", "url", "number", "select"
	Placeholder string          `json:"placeholder"` // placeholder text
	Required    bool            `json:"required"`    // whether the field is required
	Hint        string          `json:"hint"`        // optional hint below the field
	Default     string          `json:"default"`     // optional default value
	Options     []SelectOption  `json:"options"`     // options for "select" type fields
	DependsOn   *FieldCondition `json:"dependsOn"`   // show this field only when condition is met
}

// NotifyChannelTypeMeta describes a notification channel type and its form fields.
type NotifyChannelTypeMeta struct {
	Type   string                   `json:"type"`   // e.g. "gotify"
	Label  string                   `json:"label"`  // human-readable label, e.g. "Gotify"
	Fields []NotifyChannelFieldMeta `json:"fields"` // form fields for this type
}

// GetNotifyChannelTypes returns metadata for all supported notification channel types.
// Add new types here to make them available in the frontend form automatically.
func GetNotifyChannelTypes() []NotifyChannelTypeMeta {
	return []NotifyChannelTypeMeta{
		{
			Type:  "gotify",
			Label: "Gotify",
			Fields: []NotifyChannelFieldMeta{
				{
					Key:         "endpoint",
					Label:       i18n.T("notify.field.endpoint", nil),
					Type:        "url",
					Placeholder: "https://gotify.example.com",
					Required:    true,
				},
				{
					Key:         "token",
					Label:       "Token / API Key",
					Type:        "password",
					Placeholder: i18n.T("notify.field.token_placeholder_gotify", nil),
					Required:    true,
				},
			},
		},
		{
			Type:  "pushplus",
			Label: "PushPlus",
			Fields: []NotifyChannelFieldMeta{
				{
					Key:         "token",
					Label:       "Token / API Key",
					Type:        "password",
					Placeholder: i18n.T("notify.field.token_placeholder_pushplus", nil),
					Required:    true,
				},
			},
		},
		{
			Type:  "Bark",
			Label: "Bark",
			Fields: []NotifyChannelFieldMeta{
				{
					Key:         "endpoint",
					Label:       i18n.T("notify.field.endpoint", nil),
					Type:        "url",
					Placeholder: "https://api.day.app",
					Required:    true,
					Default:     "https://api.day.app",
				},
				{
					Key:         "token",
					Label:       "Token / API Key",
					Type:        "password",
					Placeholder: i18n.T("notify.field.token_placeholder_bark", nil),
					Required:    true,
				},
			},
		},
		{
			Type:  "ntfy",
			Label: "ntfy",
			Fields: []NotifyChannelFieldMeta{
				{
					Key:         "endpoint",
					Label:       i18n.T("notify.field.endpoint", nil),
					Type:        "url",
					Placeholder: "https://ntfy.sh",
					Required:    true,
					Default:     "https://ntfy.sh",
				},
				{
					Key:         "topic",
					Label:       "Topic",
					Type:        "text",
					Placeholder: i18n.T("notify.field.topic_placeholder_ntfy", nil),
					Required:    true,
				},
				{
					Key:   "auth_method",
					Label: i18n.T("notify.field.auth_method", nil),
					Type:  "select",
					Options: []SelectOption{
						{Label: i18n.T("notify.field.auth_none", nil), Value: ""},
						{Label: "Access Token (Bearer)", Value: "token"},
						{Label: i18n.T("notify.field.auth_basic", nil), Value: "basic"},
					},
					Default:  "",
					Required: false,
				},
				{
					Key:         "token",
					Label:       "Access Token",
					Type:        "password",
					Placeholder: i18n.T("notify.field.token_placeholder_ntfy", nil),
					Required:    false,
					DependsOn:   &FieldCondition{Key: "auth_method", Value: "token"},
				},
				{
					Key:         "username",
					Label:       i18n.T("notify.field.username", nil),
					Type:        "text",
					Placeholder: i18n.T("notify.field.username_placeholder_ntfy", nil),
					Required:    false,
					DependsOn:   &FieldCondition{Key: "auth_method", Value: "basic"},
				},
				{
					Key:         "password",
					Label:       i18n.T("notify.field.password", nil),
					Type:        "password",
					Placeholder: i18n.T("notify.field.password_placeholder_ntfy", nil),
					Required:    false,
					DependsOn:   &FieldCondition{Key: "auth_method", Value: "basic"},
				},
			},
		},
	}
}
