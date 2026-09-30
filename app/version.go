package main

import (
	_ "embed"
	"encoding/json"
	"runtime/debug"
	"strconv"
	"time"
)

// build-info.json holds placeholders in the repository; CI overwrites it.
//
//go:embed build-info.json
var buildInfoJSON []byte

// sourceURL is where every running instance offers its source code, as the
// AGPL (§13) requires. A modified version must point this at its own source.
const sourceURL = "https://github.com/tintamarre/bibli"

// BuildMeta is the build metadata shown on the About screen.
type BuildMeta struct {
	Version     string `json:"version"` // release tag, empty outside a release
	Commit      string `json:"commit"`
	CommitShort string `json:"commit_short"`
	CommitDate  string `json:"commit_date"` // RFC3339
	Branch      string `json:"branch"`
	Date        string `json:"date"` // build date, RFC3339
	PipelineURL string `json:"pipeline_url"`
	Repository  string `json:"repository"`
}

var buildMeta BuildMeta

func init() {
	_ = json.Unmarshal(buildInfoJSON, &buildMeta)
	// Without CI values, fall back to the VCS information Go embeds.
	repoDirty := false
	if buildMeta.Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					buildMeta.Commit = s.Value
					if len(s.Value) >= 7 {
						buildMeta.CommitShort = s.Value[:7]
					}
				case "vcs.time":
					buildMeta.CommitDate = s.Value
				case "vcs.modified":
					repoDirty = s.Value == "true"
				}
			}
		}
	}
	if buildMeta.CommitShort == "" {
		buildMeta.CommitShort = "dev"
	}

	// Here rather than as a package variable, which would run before buildMeta
	// is filled.
	assetFingerprint = displayVersion()
	if assetFingerprint == "" || assetFingerprint == "dev" || repoDirty {
		// Development or a dirty tree: a fresh fingerprint per start, or an
		// uncommitted CSS change hides behind the one-year cache.
		assetFingerprint = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
}

// assetFingerprint identifies the version of the files in static/ inside URLs.
var assetFingerprint string

// displayVersion is the version tag when there is one, otherwise the short hash.
func displayVersion() string {
	if buildMeta.Version != "" {
		return buildMeta.Version
	}
	return buildMeta.CommitShort
}
