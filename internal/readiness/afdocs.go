package readiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AfdocsResult is the subset of `afdocs check --format json --score` docsbot reads.
type AfdocsResult struct {
	URL     string `json:"url"`
	SpecURL string `json:"specUrl"`
	Results []struct {
		ID       string `json:"id"`
		Category string `json:"category"`
		Status   string `json:"status"`
		Message  string `json:"message"`
	} `json:"results"`
	Summary struct {
		Total int `json:"total"`
		Pass  int `json:"pass"`
		Warn  int `json:"warn"`
		Fail  int `json:"fail"`
		Skip  int `json:"skip"`
		Error int `json:"error"`
	} `json:"summary"`
	TestedPages int            `json:"testedPages"`
	Scoring     *AfdocsScoring `json:"scoring"`
}

// AfdocsScoring mirrors afdocs' ScoreResult.
type AfdocsScoring struct {
	Overall        float64 `json:"overall"`
	Grade          string  `json:"grade"`
	CategoryScores map[string]struct {
		Score *float64 `json:"score"`
		Grade *string  `json:"grade"`
	} `json:"categoryScores"`
	CheckScores map[string]struct {
		EarnedScore      float64 `json:"earnedScore"`
		MaxScore         float64 `json:"maxScore"`
		ScoreDisplayMode string  `json:"scoreDisplayMode"`
	} `json:"checkScores"`
	Cap *struct {
		Cap     float64 `json:"cap"`
		CheckID string  `json:"checkId"`
		Reason  string  `json:"reason"`
	} `json:"cap"`
	Diagnostics []struct {
		ID         string `json:"id"`
		Severity   string `json:"severity"`
		Message    string `json:"message"`
		Resolution string `json:"resolution"`
	} `json:"diagnostics"`
	Resolutions map[string]string `json:"resolutions"`
}

// ErrAfdocsMissing means the afdocs command could not be found.
var ErrAfdocsMissing = errors.New("afdocs not available")

// AfdocsAvailable reports whether the first word of cmd is on PATH.
func AfdocsAvailable(cmd string) bool {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return false
	}
	_, err := exec.LookPath(f[0])
	return err == nil
}

// RunAfdocs runs afdocs against url and parses its JSON. afdocs exits non-zero
// when checks fail, so a parsable report on stdout counts as success.
func RunAfdocs(ctx context.Context, cmd, url string, extra []string, timeout time.Duration) (*AfdocsResult, error) {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return nil, ErrAfdocsMissing
	}
	if _, err := exec.LookPath(f[0]); err != nil {
		return nil, fmt.Errorf("%w: %s not on PATH", ErrAfdocsMissing, f[0])
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(f[1:], "check", url, "--format", "json", "--score", "--quiet")
	args = append(args, extra...)
	c := exec.CommandContext(ctx, f[0], args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	runErr := c.Run()
	out := bytes.TrimSpace(stdout.Bytes())
	if i := bytes.IndexByte(out, '{'); i > 0 {
		out = out[i:] // npx may print notices before the JSON
	}
	var res AfdocsResult
	if len(out) > 0 && json.Unmarshal(out, &res) == nil && (len(res.Results) > 0 || res.Scoring != nil) {
		return &res, nil
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 500 {
		msg = msg[len(msg)-500:]
	}
	if runErr != nil {
		return nil, fmt.Errorf("afdocs failed: %v: %s", runErr, msg)
	}
	return nil, fmt.Errorf("afdocs returned no JSON report: %s", msg)
}

// RawScore is afdocs' score before any cap: earned over possible points,
// leaving out checks that did not apply.
func (s *AfdocsScoring) RawScore() float64 {
	if s == nil {
		return 0
	}
	if s.Cap == nil || len(s.CheckScores) == 0 {
		return s.Overall
	}
	var earned, max float64
	for _, c := range s.CheckScores {
		if c.ScoreDisplayMode == "notApplicable" {
			continue
		}
		earned += c.EarnedScore
		max += c.MaxScore
	}
	if max == 0 {
		return s.Overall
	}
	return 100 * earned / max
}
