package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

const maxSubmitBytes = 4 << 20

type submitStdin struct {
	Stage   string            `json:"stage"`
	Results []json.RawMessage `json:"results"`
}

type submitResult struct {
	Item     string             `json:"item"`
	Accepted int                `json:"accepted"`
	Rejected []bootstrap.Reject `json:"rejected"`
	Findings []string           `json:"findings"`
	Requeued bool               `json:"requeued,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type submitReport struct {
	RunID    string         `json:"run_id"`
	Results  []submitResult `json:"results"`
	Accepted int            `json:"accepted"`
	Rejected int            `json:"rejected"`
	Status   string         `json:"status"`
}

func bootstrapSubmit(ctx context.Context, env *Env, args []string) (Result, error) {
	fl, err := parseFlags(args, map[string]bool{"run": true, "batch": true})
	if err != nil {
		return Result{}, err
	}
	runID, batch := oneFlag(fl, "run"), oneFlag(fl, "batch")
	if runID == "" || batch == "" {
		return Result{}, &UsageError{Msg: "usage: wikory bootstrap submit --run RUN --batch BATCH  (results as JSON on stdin)"}
	}
	raw, err := io.ReadAll(io.LimitReader(env.IO.In, maxSubmitBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(raw) > maxSubmitBytes {
		return Result{}, &UsageError{Msg: fmt.Sprintf("the submission is larger than %d bytes; submit fewer items per call", maxSubmitBytes)}
	}
	var in submitStdin
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return Result{}, &UsageError{Msg: "stdin is not the expected JSON: " + jsonProblem(err)}
	}
	// Anything after the object, a second object included, is refused and never repeated.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Result{}, &UsageError{Msg: "stdin has data after the JSON object"}
	}
	if in.Stage != "extract" && in.Stage != "verify" {
		return Result{}, &UsageError{Msg: `stage must be "extract" or "verify"`}
	}
	p, runs, _, err := openRun(ctx, env, nil, map[string]bool{})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if _, err := runs.Get(ctx, runID); err != nil {
		return Result{}, err
	}
	wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
	// Every result is read and checked before any is submitted, so a bad one later in the batch
	// cannot leave earlier ones committed with no report.
	var props []bootstrap.Proposal
	var sets []bootstrap.VerdictSet
	for _, rawRes := range in.Results {
		var item string
		var usage bootstrap.Usage
		if in.Stage == "extract" {
			var prop bootstrap.Proposal
			if err := strictUnmarshal(rawRes, &prop); err != nil {
				return Result{}, &UsageError{Msg: "an extract result is not the expected JSON: " + jsonProblem(err)}
			}
			props, item, usage = append(props, prop), prop.Item, prop.Usage
		} else {
			var vs bootstrap.VerdictSet
			if err := strictUnmarshal(rawRes, &vs); err != nil {
				return Result{}, &UsageError{Msg: "a verify result is not the expected JSON: " + jsonProblem(err)}
			}
			sets, item, usage = append(sets, vs), vs.Item, vs.Usage
		}
		if err := usage.Check(); err != nil {
			return Result{}, &UsageError{Msg: "a " + in.Stage + " result has usage the driver cannot have spent: " + err.Error()}
		}
		if it, err := runs.Item(ctx, item); err == nil && it.RunID != runID {
			return Result{}, &UsageError{Msg: "an item in the submission does not belong to the run given with --run"}
		} else if err != nil && !errors.Is(err, bootstrap.ErrNoItem) {
			return Result{}, err
		}
	}
	rep := submitReport{RunID: runID, Results: []submitResult{}}
	var notices []string
	for i := range in.Results {
		var sr submitResult
		switch in.Stage {
		case "extract":
			r, err := bootstrap.Submit(ctx, bootstrap.SubmitInput{Runs: runs, Env: p.validateEnv(), Work: wf, Mint: ids.NewULID, Batch: batch, P: props[i]})
			sr = submitResult{Item: r.Item, Accepted: r.Accepted, Rejected: r.Rejected, Findings: r.Findings, Requeued: r.Requeued}
			if err != nil {
				sr.Error = describeSubmitError(err)
			}
		case "verify":
			r, err := bootstrap.ApplyVerdicts(ctx, bootstrap.VerifyInput{Runs: runs, Env: p.validateEnv(), Mint: ids.NewULID, Batch: batch, V: sets[i],
				Blob: func(oid string) ([]byte, error) { return p.Repo.ReadBlob(ctx, oid, bootstrap.MaxFileBytes) }})
			sr = submitResult{Item: r.Item, Accepted: r.Confirmed + r.Contradicted, Rejected: r.Rejected, Findings: r.Findings, Requeued: r.Requeued}
			if err != nil {
				sr.Error = describeSubmitError(err)
			}
		}
		if sr.Rejected == nil {
			sr.Rejected = []bootstrap.Reject{}
		}
		if sr.Findings == nil {
			sr.Findings = []string{}
		}
		rep.Accepted += sr.Accepted
		rep.Rejected += len(sr.Rejected)
		if sr.Error != "" {
			notices = append(notices, noticeFor(sr))
		}
		rep.Results = append(rep.Results, sr)
	}
	if st, err := runs.Status(ctx, runID); err == nil {
		rep.Status = statusLine(st)
	}
	return Result{Data: rep, Notices: notices, Text: fmt.Sprintf("%d accepted, %d rejected. %s\n", rep.Accepted, rep.Rejected, rep.Status)}, nil
}

// noticeFor names the item only when the store found it; a submission naming an item that does
// not exist has no item to name, and its id is never repeated.
func noticeFor(sr submitResult) string {
	if sr.Item == "" {
		return "a submitted item: " + sr.Error
	}
	return fmt.Sprintf("item %s: %s", sr.Item, sr.Error)
}

func strictUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// jsonProblem says what is wrong with a JSON submission without repeating any of its text: the
// name of an unknown field, for one, is chosen by whoever wrote the JSON, and the message is
// printed into the working agent's transcript.
func jsonProblem(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("invalid JSON at byte %d", syn.Offset)
	case errors.As(err, &typ):
		return "a value of the wrong type"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "the JSON ends early"
	default:
		return "an unknown field or other text that is not part of the schema"
	}
}

func describeSubmitError(err error) string {
	if errors.Is(err, bootstrap.ErrStaleLease) {
		return "refused: the lease on this item expired or belongs to another batch; ask for new work with wikory bootstrap next"
	}
	return bootstrap.Safe(err.Error())
}
