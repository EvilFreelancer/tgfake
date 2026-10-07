package server

// apiResponse is the envelope every Bot API method answers with.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      any             `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  *responseParams `json:"parameters,omitempty"`
}

// responseParams carries retry_after on a 429.
type responseParams struct {
	RetryAfter int `json:"retry_after,omitempty"`
}
