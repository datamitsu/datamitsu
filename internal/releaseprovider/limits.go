package releaseprovider

import "time"

const (
	metadataMaxBytes = 16 * 1024 * 1024
	apiPageSize      = 100
	apiMaxPages      = 10
	apiTimeout       = 30 * time.Second
)
