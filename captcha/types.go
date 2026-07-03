package captcha

import (
	"encoding/json"
	"fmt"
)

// =============================================================================
// CaptchaType — 0=UNKNOWN, 1=SLIDE, 2=CLICK（与 C 头文件一致）
// =============================================================================

// CaptchaType 表示极验验证码的类型。
type CaptchaType int32

const (
	TypeUnknown CaptchaType = 0
	TypeSlide   CaptchaType = 1
	TypeClick   CaptchaType = 2
)

func (t CaptchaType) String() string {
	switch t {
	case TypeSlide:
		return "slide"
	case TypeClick:
		return "click"
	default:
		return "unknown"
	}
}

// =============================================================================
// 公开数据类型
// =============================================================================

// VersionInfo DLL 版本信息。
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
}

// GeetestCS C/S 参数（C 字段已从 hex 解码）。
type GeetestCS struct {
	S string // s 参数
	C []byte // c 参数（原始字节）
}

// NewCSArgs 新一轮挑战参数。根据验证码类型（click/slide）不同字段生效。
type NewCSArgs struct {
	C            []byte // 原始 c 数据（hex 解码后）
	S            string // s 参数
	NewChallenge string // slide：新的 challenge
	FullBgURL    string // slide：完整背景图 URL
	MissBgURL    string // slide：缺失背景图 URL
	SliderURL    string // slide：滑块图 URL
	PicURL       string // click：背景图 URL（用于坐标计算）
}

// VerifyResult 验证 w 参数的结果。
type VerifyResult struct {
	Message  string `json:"message"`
	Validate string `json:"validate"`
}

// =============================================================================
// 内部 JSON 解析类型
// =============================================================================

type jsonEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func parseEnvelope(raw string) (json.RawMessage, error) {
	if raw == "" {
		return nil, fmt.Errorf("captcha: 空响应")
	}
	var env jsonEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("captcha: 解析响应失败: %w (raw: %.200s)", err, raw)
	}
	if !env.Success {
		msg := env.Error
		if msg == "" {
			msg = "未知错误"
		}
		return nil, fmt.Errorf("captcha: %s", msg)
	}
	return env.Data, nil
}

func parseData[T any](raw string) (T, error) {
	var zero T
	data, err := parseEnvelope(raw)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, fmt.Errorf("captcha: 解析 data 字段失败: %w", err)
	}
	return v, nil
}
