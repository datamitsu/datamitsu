package github

import (
	"time"

	"github.com/datamitsu/datamitsu/internal/releaseasset"
)

func normalizeSemver(tag string) (string, bool) { return releaseasset.NormalizeSemver(tag) }

func selectLatestStableRelease(releases []Release, minAgeMinutes int, now time.Time) *Release {
	return releaseasset.SelectLatestStableRelease(releases, minAgeMinutes, now)
}
