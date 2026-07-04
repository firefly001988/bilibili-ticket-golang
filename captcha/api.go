package captcha

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// =============================================================================
// C 函数指针（由 registerAll 绑定）
// =============================================================================

var (
	cFreeString        func(ptr *byte)
	cVersion           func() *byte
	cGetType           func(gt, challenge, w string) int32
	cSolve             func(gt, challenge string) *byte
	cSolveClick        func(gt, challenge string) *byte
	cSolveSlide        func(gt, challenge string) *byte
	cGetCS             func(gt, challenge, w string) *byte
	cGetNewCSArgsClick func(gt, challenge string) *byte
	cGetNewCSArgsSlide func(gt, challenge string) *byte
	cCalculateKeyClick func(picURL string) *byte
	cCalculateKeySlide func(fullBg, missBg, slider string) *byte
	cGenerateWClick    func(key, gt, challenge string, c unsafe.Pointer, cLen int32, s string) *byte
	cGenerateWSlide    func(key, gt, challenge string, c unsafe.Pointer, cLen int32, s string) *byte
	cVerify            func(gt, challenge, w string) *byte
	cWarmup            func() *byte
)

// =============================================================================
// 初始化
// =============================================================================

var (
	handle   uintptr
	initOnce sync.Once
	initErr  error
)

// Init 加载动态链接库并注册所有 C 函数。必须在使用其他函数前调用一次。
// rootPath 为库文件所在目录的路径。
func Init(rootPath string) error {
	initOnce.Do(func() {
		libPath := fmt.Sprintf("%s/%s", rootPath, getSystemLibraryPath())
		h, err := openLibrary(libPath)
		if err != nil {
			initErr = fmt.Errorf("captcha: 加载 %s 失败: %w", libPath, err)
			return
		}
		handle = h
		initErr = registerAll()
	})
	return initErr
}

// IsAvailable 检查库文件是否存在于 rootPath 目录中，不实际加载。
func IsAvailable(rootPath string) bool {
	libPath := fmt.Sprintf("%s/%s", rootPath, getSystemLibraryPath())
	_, err := os.Stat(libPath)
	return err == nil
}

func registerAll() error {
	purego.RegisterLibFunc(&cFreeString, handle, "captcha_free_string")
	purego.RegisterLibFunc(&cVersion, handle, "captcha_version")
	purego.RegisterLibFunc(&cGetType, handle, "captcha_get_type")
	purego.RegisterLibFunc(&cSolve, handle, "captcha_solve")
	purego.RegisterLibFunc(&cSolveClick, handle, "captcha_solve_click")
	purego.RegisterLibFunc(&cSolveSlide, handle, "captcha_solve_slide")
	purego.RegisterLibFunc(&cGetCS, handle, "captcha_get_cs")
	purego.RegisterLibFunc(&cGetNewCSArgsClick, handle, "captcha_get_new_cs_args_click")
	purego.RegisterLibFunc(&cGetNewCSArgsSlide, handle, "captcha_get_new_cs_args_slide")
	purego.RegisterLibFunc(&cCalculateKeyClick, handle, "captcha_calculate_key_click")
	purego.RegisterLibFunc(&cCalculateKeySlide, handle, "captcha_calculate_key_slide")
	purego.RegisterLibFunc(&cGenerateWClick, handle, "captcha_generate_w_click")
	purego.RegisterLibFunc(&cGenerateWSlide, handle, "captcha_generate_w_slide")
	purego.RegisterLibFunc(&cVerify, handle, "captcha_verify")
	registerOptionalLibFunc(&cWarmup, handle, "captcha_warmup")

	if cFreeString == nil {
		return fmt.Errorf("captcha: captcha_free_string 未绑定——库可能不兼容")
	}
	if cVersion == nil {
		return fmt.Errorf("captcha: captcha_version 未绑定——库可能不兼容")
	}
	return nil
}

func registerOptionalLibFunc(fptr any, handle uintptr, name string) {
	defer func() {
		_ = recover()
	}()
	purego.RegisterLibFunc(fptr, handle, name)
}

// =============================================================================
// 内部辅助函数
// =============================================================================

// copyAndFree 将 C 字符串拷贝到 Go 堆，然后释放 C 内存。ptr 为 nil 时返回 ""。
func copyAndFree(ptr *byte) string {
	if ptr == nil {
		return ""
	}
	n := 0
	for p := unsafe.Pointer(ptr); *(*byte)(unsafe.Add(p, n)) != 0; n++ {
	}
	s := string(unsafe.Slice(ptr, n))
	cFreeString(ptr)
	return s
}

// bytesDataPtr 返回切片底层数据的指针，空切片返回 nil。
func bytesDataPtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

// =============================================================================
// 公开 API —— 直接映射 C 头文件中的 14 个函数
// =============================================================================

// Version 返回 DLL 的版本信息。
func Version() (*VersionInfo, error) {
	return parseData[*VersionInfo](copyAndFree(cVersion()))
}

// GetType 返回验证码类型（0=UNKNOWN, 1=SLIDE, 2=CLICK）。
// w 可为空字符串（等价于 C 端的 NULL）。
func GetType(gt, challenge, w string) (CaptchaType, error) {
	return CaptchaType(cGetType(gt, challenge, w)), nil
}

// Solve 自动识别验证码类型并求解，返回 validate 字符串。
func Solve(gt, challenge string) (string, error) {
	return parseData[string](copyAndFree(cSolve(gt, challenge)))
}

// SolveClick 求解 click 类型验证码，返回 validate 字符串。
func SolveClick(gt, challenge string) (string, error) {
	return parseData[string](copyAndFree(cSolveClick(gt, challenge)))
}

// SolveSlide 求解 slide 类型验证码，返回 validate 字符串。
func SolveSlide(gt, challenge string) (string, error) {
	return parseData[string](copyAndFree(cSolveSlide(gt, challenge)))
}

// GetCS 获取初始 C/S 参数。w 可为空字符串。
func GetCS(gt, challenge, w string) (*GeetestCS, error) {
	type csData struct {
		C string `json:"c"`
		S string `json:"s"`
	}
	jd, err := parseData[csData](copyAndFree(cGetCS(gt, challenge, w)))
	if err != nil {
		return nil, err
	}
	cBytes, err := hex.DecodeString(jd.C)
	if err != nil {
		return nil, fmt.Errorf("captcha: C 字段 hex 解码失败: %w", err)
	}
	return &GeetestCS{S: jd.S, C: cBytes}, nil
}

// GetNewCSArgsClick 获取 click 类型的新一轮挑战参数。
func GetNewCSArgsClick(gt, challenge string) (*NewCSArgs, error) {
	type data struct {
		C      string `json:"c"`
		S      string `json:"s"`
		PicURL string `json:"pic_url"`
	}
	jd, err := parseData[data](copyAndFree(cGetNewCSArgsClick(gt, challenge)))
	if err != nil {
		return nil, err
	}
	cBytes, err := hex.DecodeString(jd.C)
	if err != nil {
		return nil, fmt.Errorf("captcha: C 字段 hex 解码失败: %w", err)
	}
	return &NewCSArgs{C: cBytes, S: jd.S, PicURL: jd.PicURL}, nil
}

// GetNewCSArgsSlide 获取 slide 类型的新一轮挑战参数。
func GetNewCSArgsSlide(gt, challenge string) (*NewCSArgs, error) {
	type data struct {
		C            string `json:"c"`
		S            string `json:"s"`
		NewChallenge string `json:"new_challenge"`
		FullBgURL    string `json:"full_bg_url"`
		MissBgURL    string `json:"miss_bg_url"`
		SliderURL    string `json:"slider_url"`
	}
	jd, err := parseData[data](copyAndFree(cGetNewCSArgsSlide(gt, challenge)))
	if err != nil {
		return nil, err
	}
	cBytes, err := hex.DecodeString(jd.C)
	if err != nil {
		return nil, fmt.Errorf("captcha: C 字段 hex 解码失败: %w", err)
	}
	return &NewCSArgs{
		C:            cBytes,
		S:            jd.S,
		NewChallenge: jd.NewChallenge,
		FullBgURL:    jd.FullBgURL,
		MissBgURL:    jd.MissBgURL,
		SliderURL:    jd.SliderURL,
	}, nil
}

// CalculateKeyClick 计算 click 类型的关键位置参数。
func CalculateKeyClick(picURL string) (string, error) {
	return parseData[string](copyAndFree(cCalculateKeyClick(picURL)))
}

// CalculateKeySlide 计算 slide 类型的滑块距离。
func CalculateKeySlide(fullBg, missBg, slider string) (string, error) {
	return parseData[string](copyAndFree(cCalculateKeySlide(fullBg, missBg, slider)))
}

// GenerateWClick 生成 click 类型的 w 参数。
func GenerateWClick(key, gt, challenge string, c []byte, s string) (string, error) {
	return parseData[string](copyAndFree(cGenerateWClick(key, gt, challenge, bytesDataPtr(c), int32(len(c)), s)))
}

// GenerateWSlide 生成 slide 类型的 w 参数。
func GenerateWSlide(key, gt, challenge string, c []byte, s string) (string, error) {
	return parseData[string](copyAndFree(cGenerateWSlide(key, gt, challenge, bytesDataPtr(c), int32(len(c)), s)))
}

// Verify 提交 w 参数进行验证，返回验证结果。
// w 可为空字符串。
func Verify(gt, challenge, w string) (*VerifyResult, error) {
	return parseData[*VerifyResult](copyAndFree(cVerify(gt, challenge, w)))
}

// Warmup 预热验证码模块（加载 ONNX 模型、初始化推理引擎）。
// 建议在服务启动时调用一次，避免首次请求耗时过长。
func Warmup() error {
	if cWarmup == nil {
		return fmt.Errorf("captcha: captcha_warmup 未绑定——库版本不支持预热")
	}
	_, err := parseData[string](copyAndFree(cWarmup()))
	return err
}
