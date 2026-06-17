package api

type GetAxeResponse struct {
	Version   string `json:"version"`
	PublicKey string `json:"public_key"`
	Deadline  int64  `json:"deadline"`
}
