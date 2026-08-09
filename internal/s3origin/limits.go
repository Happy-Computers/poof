package s3origin

// Hard limits for the S3 range origin. Change only by deliberate redesign.

const (
	MaxRangeBytes          = 8 * 1024 * 1024
	MaxConcurrentS3Fetches = 4
	// MinCatalogRefresh is the minimum time between ListObjectsV2 refreshes.
	MinCatalogRefresh = 1 // seconds — used as time.Duration * Second in callers
)
