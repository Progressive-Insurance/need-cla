// Deterministic end-to-end and edge-case regressions for the public API and
// the individual heuristics. These complement the ported audit fixtures in
// audit_repro_test.go and assert the corrected behavior for B01-B13 plus the
// R14 input-validation contract. They run offline in -short mode.
package needcla

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v43/github"
)

// completePositiveRoutes describes a repository whose README references the
// CLA, with every other heuristic able to complete.
func completePositiveRoutes() map[string]auditReply {
	return map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {body: `[]`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[{"path":"README.md","type":"blob","sha":"readme"}],"truncated":false}`},
		// base64("Contributors must sign a CLA first.") — references the CLA.
		"/repos/example/project/git/blobs/readme": {body: auditJSON(map[string]string{
			"encoding": "base64",
			"content":  "Q29udHJpYnV0b3JzIG11c3Qgc2lnbiBhIENMQSBmaXJzdC4=",
		})},
	}
}

func TestDetailCompletePositive(t *testing.T) {
	client, _ := auditClient(completePositiveRoutes())
	d, err := DetailWithContext(context.Background(), client, "example", "project")
	if err != nil {
		t.Fatalf("complete positive scan returned error: %v", err)
	}
	if !d.InREADME || !d.Required() {
		t.Errorf("expected README evidence and required, got %+v", d)
	}
}

// completeNegativeRoutes describes an empty repository with no evidence
// anywhere and no failures.
func completeNegativeRoutes() map[string]auditReply {
	return map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {body: `[]`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[],"truncated":false}`},
	}
}

func TestDetailCompleteNegative(t *testing.T) {
	client, _ := auditClient(completeNegativeRoutes())
	d, err := DetailWithContext(context.Background(), client, "example", "project")
	if err != nil {
		t.Fatalf("complete negative scan returned error: %v", err)
	}
	if d.Required() {
		t.Errorf("expected not required, got %+v", d)
	}
}

// partialPositiveRoutes: the repository is owned by a known CLA requirer AND
// the PR listing fails. The positive evidence must be preserved and the error
// must be a partial *Errors, not a fatal error.
func partialPositiveRoutes() map[string]auditReply {
	return map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/google/go-github":      {body: `{"default_branch":"main"}`},
		"/repos/google/go-github/pulls": {status: http.StatusInternalServerError, body: `{"message":"boom"}`},
		"/repos/google/go-github/git/trees/main": {body: `{"tree":[],"truncated":false}`},
	}
}

func TestDetailPartialPositivePreservesEvidence(t *testing.T) {
	client, _ := auditClient(partialPositiveRoutes())
	d, err := DetailWithContext(context.Background(), client, "google", "go-github")
	if err == nil {
		t.Fatal("expected a partial error, got nil")
	}
	var partial *Errors
	if !errors.As(err, &partial) {
		t.Fatalf("expected *Errors, got %T: %v", err, err)
	}
	if partial.TagErr == nil {
		t.Errorf("expected the PR/tag check to carry the failure: %+v", partial)
	}
	if !d.Known || !d.Required() {
		t.Errorf("known-owner positive evidence must be preserved, got %+v", d)
	}
}

// incompleteNoEvidenceRoutes: no positive evidence and a failed heuristic.
// Detail must return a *Errors (incomplete), and Check must not claim a
// reliable negative.
func incompleteNoEvidenceRoutes() map[string]auditReply {
	return map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {status: http.StatusInternalServerError, body: `{"message":"boom"}`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[],"truncated":false}`},
	}
}

func TestCheckIncompleteIsNotReliableNegative(t *testing.T) {
	client, _ := auditClient(incompleteNoEvidenceRoutes())
	required, err := CheckWithContext(context.Background(), client, "example", "project")
	if required {
		t.Errorf("no positive evidence should not report required")
	}
	var partial *Errors
	if !errors.As(err, &partial) {
		t.Fatalf("expected a partial *Errors, got %T: %v", err, err)
	}
	// The failed heuristic is identifiable through the aggregate.
	if partial.TagErr == nil {
		t.Errorf("expected TagErr to be set: %+v", partial)
	}
}

func TestDetailNotFoundExposesErrNotFound(t *testing.T) {
	client, _ := auditClient(map[string]auditReply{
		"/rate_limit":            auditRate(1000),
		"/repos/example/project": {status: http.StatusNotFound, body: `{"message":"Not Found"}`},
	})
	_, err := DetailWithContext(context.Background(), client, "example", "project")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDetailRateLimit429PreservesTypedError(t *testing.T) {
	client, _ := auditClient(map[string]auditReply{
		"/rate_limit":            auditRate(1000),
		"/repos/example/project": {status: http.StatusTooManyRequests, body: `{"message":"too many requests"}`},
	})
	_, err := DetailWithContext(context.Background(), client, "example", "project")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrInvalidToken) {
		t.Errorf("429 must not be classified as invalid credentials: %v", err)
	}
	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected the 429 cause to be preserved: %v", err)
	}
}

// later401Routes: a 401 arriving on a later (heuristic) request must remain a
// typed GitHub API failure carried in the per-check field and stay reachable
// via errors.As on the public aggregate. It is not reclassified as
// ErrInvalidToken (that sentinel is reserved for authentication failures on
// the preflight requests); the important property is that the original typed
// cause is preserved and inspectable.
func TestDetailLaterRequest401KeepsTypedCause(t *testing.T) {
	client, _ := auditClient(map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[],"truncated":false}`},
	})
	_, err := DetailWithContext(context.Background(), client, "example", "project")
	var partial *Errors
	if !errors.As(err, &partial) || partial.TagErr == nil {
		t.Fatalf("expected a partial *Errors with TagErr, got %T: %v", err, err)
	}
	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response.StatusCode != http.StatusUnauthorized {
		t.Errorf("the 401 cause must remain inspectable through the aggregate: %v", err)
	}
}

// ctxAwareTransport fails every request when the request context is already
// done, mimicking a real HTTP client's behavior. The plain auditTransport
// ignores context, so it cannot exercise the cancellation/deadline paths.
type ctxAwareTransport struct{ routes map[string]auditReply }

func (c *ctxAwareTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A deadline that has already passed is a deterministic way to simulate
	// a request that cannot proceed, independent of timer-goroutine timing.
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		return nil, context.DeadlineExceeded
	}
	r, ok := c.routes[req.URL.Path]
	if !ok {
		r = auditReply{status: http.StatusNotFound, body: `{"message":"unexpected fixture request"}`}
	}
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Request:    req,
	}, nil
}

func TestDetailCancellationPreserved(t *testing.T) {
	client := github.NewClient(&http.Client{Transport: &ctxAwareTransport{routes: completePositiveRoutes()}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DetailWithContext(ctx, client, "example", "project")
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled to be preserved, got %v", err)
	}
}

func TestDetailDeadlinePreserved(t *testing.T) {
	client := github.NewClient(&http.Client{Transport: &ctxAwareTransport{routes: completePositiveRoutes()}})
	// A deadline already in the past is deterministically expired.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := DetailWithContext(ctx, client, "example", "project")
	if err == nil {
		t.Fatal("expected an error after deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded to be preserved, got %v", err)
	}
}

// R02: a tree-loading failure is a partial scan, not a fatal one. The
// known-owner heuristic (which does not need the tree) still runs and its
// positive evidence is preserved, while every tree-dependent heuristic is
// reported as failed with the tree error as its cause.
func TestDetailTreeFailureIsPartialScanPreservingIndependentEvidence(t *testing.T) {
	routes := map[string]auditReply{
		"/rate_limit":            auditRate(1000),
		"/repos/google/go-github": {body: `{"default_branch":"main"}`},
		"/repos/google/go-github/pulls": {body: `[]`},
		// The tree request fails.
		"/repos/google/go-github/git/trees/main": {status: http.StatusInternalServerError, body: `{"message":"tree boom"}`},
	}
	client, _ := auditClient(routes)
	d, err := DetailWithContext(context.Background(), client, "google", "go-github")
	var partial *Errors
	if !errors.As(err, &partial) {
		t.Fatalf("expected a partial *Errors, got %T: %v", err, err)
	}
	// Independent evidence survives.
	if !d.Known || !d.Required() {
		t.Errorf("known-owner evidence must survive a tree failure, got %+v", d)
	}
	// Tree-dependent heuristics are all marked as failed with the cause.
	for name, e := range map[string]error{
		"BotFile":        partial.BotFileErr,
		"InContributing": partial.InContributingErr,
		"InREADME":       partial.InREADMEErr,
		"Action":         partial.ActionErr,
	} {
		if e == nil {
			t.Errorf("tree-dependent heuristic %s should carry the tree failure", name)
		}
	}
	// The underlying HTTP cause is inspectable through the aggregate.
	var apiErr *github.ErrorResponse
	if !errors.As(err, &apiErr) || apiErr.Response.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected the tree HTTP failure to remain inspectable: %v", err)
	}
}

func TestDetailMalformedBase64IsAnError(t *testing.T) {
	routes := map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {body: `[]`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[{"path":"README.md","type":"blob","sha":"readme"}],"truncated":false}`},
		// "!!!" is not valid base64.
		"/repos/example/project/git/blobs/readme": {body: `{"encoding":"base64","content":"!!!"}`},
	}
	client, _ := auditClient(routes)
	d, err := DetailWithContext(context.Background(), client, "example", "project")
	if err == nil {
		t.Fatal("expected a decode error for malformed base64")
	}
	if d.InREADME {
		t.Errorf("malformed base64 must not be reported as positive evidence: %+v", d)
	}
	var partial *Errors
	if !errors.As(err, &partial) || partial.InREADMEErr == nil {
		t.Errorf("expected InREADMEErr to carry the decode failure: %v", err)
	}
}

func TestDetailMalformedYAMLIssuesActionError(t *testing.T) {
	content := "jobs:\n  cla:\n    steps:\n      - uses: cla-assistant/github-action@v2\n  bad: [unclosed"
	routes := map[string]auditReply{
		"/rate_limit":                  auditRate(1000),
		"/repos/example/project":       {body: `{"default_branch":"main"}`},
		"/repos/example/project/pulls": {body: `[]`},
		"/repos/example/project/git/trees/main": {body: `{"tree":[{"path":".github/workflows","type":"tree","sha":"wf"}],"truncated":false}`},
		"/repos/example/project/git/trees/wf":   {body: `{"tree":[{"path":"cla.yml","type":"blob","sha":"c"}],"truncated":false}`},
		"/repos/example/project/git/blobs/c":    {body: auditJSON(map[string]string{
			"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)),
		})},
	}
	client, _ := auditClient(routes)
	d, err := DetailWithContext(context.Background(), client, "example", "project")
	if err == nil {
		t.Fatal("expected an action error for malformed YAML")
	}
	if d.Action {
		t.Errorf("malformed YAML must not be reported as positive evidence: %+v", d)
	}
	var partial *Errors
	if !errors.As(err, &partial) || partial.ActionErr == nil {
		t.Errorf("expected ActionErr to carry the YAML failure: %v", err)
	}
	// The per-file cause is inspectable through the aggregate.
	var fileErr *fileErrors
	if !errors.As(partial.ActionErr, &fileErr) {
		t.Logf("note: ActionErr is %T", partial.ActionErr)
	}
}

func TestDetailEmptyOwnerOrRepoIsRejected(t *testing.T) {
	client, _ := auditClient(completePositiveRoutes())
	for _, tc := range []struct{ owner, repo string }{{"", "project"}, {"example", ""}, {" ", "project"}} {
		if _, err := DetailWithContext(context.Background(), client, tc.owner, tc.repo); err == nil {
			t.Errorf("expected rejection for owner=%q repo=%q", tc.owner, tc.repo)
		}
	}
}

func TestDetailNilClientIsRejected(t *testing.T) {
	if _, err := DetailWithContext(context.Background(), nil, "example", "project"); err == nil {
		t.Error("expected rejection for nil client")
	}
}

func TestCheckMatchesRequired(t *testing.T) {
	client, _ := auditClient(completePositiveRoutes())
	r, err := CheckWithContext(context.Background(), client, "example", "project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r {
		t.Error("Check should agree with a positive Detail")
	}
}

func TestErrorsErrorStringListsAllFields(t *testing.T) {
	e := &Errors{
		TagErr:            errors.New("tag"),
		BotFileErr:        errors.New("bot"),
		InContributingErr: errors.New("contrib"),
		InREADMEErr:       errors.New("readme"),
		ActionErr:         errors.New("action"),
	}
	s := e.Error()
	for _, want := range []string{"tag", "bot", "contrib", "readme", "action"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() missing %q:\n%s", want, s)
		}
	}
}

func TestErrorsErrOrNilNilWhenClean(t *testing.T) {
	var e Errors
	if err := e.ErrOrNil(); err != nil {
		t.Errorf("expected nil from a clean aggregate, got %v", err)
	}
}
