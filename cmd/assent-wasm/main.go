//go:build js && wasm

// Command assent-wasm is the browser playground's engine: the assent approval
// and escalation core compiled to WebAssembly, exposing a small JSON API on the
// global "assent" object. It has no backend — every call runs in the visitor's
// tab over the pure library, and nothing leaves the page.
//
// assent is stateful: an Engine drives a request through its chain, decision by
// decision, over a store and an audit log. The bridge therefore holds a live
// session — an Engine, its MemStore, a chronicle.Log, and a Delegations resolver
// — in a Go map keyed by a session id the page passes. A call mutates the live
// engine and returns the whole fresh state plus the full audit trail, so the
// page re-renders from one value. Reload, or start a new scenario, to reset.
//
// The three composed siblings are visible across the boundary on purpose: the
// lifecycle state is the fsmx machine's state, the escalation clock is a sundial
// timer advanced by Tick over a business schedule, and the trail is the
// append-only chronicle log read back by Trail.
//
// Two error channels are kept apart. A malformed call — an unknown session, a
// bad enum, unparseable JSON — returns the house {error, code} object (the Go
// error path). A decision the engine correctly rejects — four-eyes, no
// delegation in force, not an approver — is an expected, teachable outcome of a
// well-formed call, so it rides back inside the state as {error, code} beside
// the unchanged live state, and the page shows it without losing the chain.
//
// This file carries the js && wasm build constraint so the native toolchain —
// go build ./..., go vet ./..., go test ./..., golangci-lint — never tries to
// compile it (syscall/js exists only under GOOS=js). The command imports only
// assent and its three first-party siblings, which the root module already
// requires; it adds nothing to go.sum.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"syscall/js"
	"time"

	"github.com/zkrebbekx/assent"
	"github.com/zkrebbekx/sundial"
)

// ctx is the background context every engine call runs under. The engine owns no
// clock and no goroutine, so a plain background context is enough.
var ctx = context.Background()

func main() {
	obj := map[string]any{}
	register(obj, "newSession", handleNewSession)
	register(obj, "decide", handleDecide)
	register(obj, "withdraw", handleWithdraw)
	register(obj, "reassign", handleReassign)
	register(obj, "tick", handleTick)
	register(obj, "addDelegation", handleAddDelegation)
	register(obj, "state", handleState)
	register(obj, "loadScenario", handleLoadScenario)
	obj["scenarios"] = toJS(scenarioList())

	js.Global().Set("assent", js.ValueOf(obj))
	js.Global().Set("__assentReady", js.ValueOf(true))
	if cb := js.Global().Get("__assentOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	// Keep the Go runtime alive for the lifetime of the page.
	select {}
}

// register wraps a handler so every call recovers from a panic into a returned
// {error} object rather than tearing down the wasm instance, and marshals the
// result to a real JS object the page can read without parsing.
func register(obj map[string]any, name string, fn func([]byte) (any, error)) {
	obj[name] = js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
		defer func() {
			if r := recover(); r != nil {
				result = toJS(map[string]any{"error": fmt.Sprintf("panic: %v", r), "code": "panic"})
			}
		}()
		out, err := fn(input(args))
		if err != nil {
			return toJS(errObj(err))
		}
		return toJS(out)
	})
}

// input normalises the first argument to JSON bytes. A JS object is stringified;
// a bare string is passed through verbatim (loadScenario accepts one); anything
// absent becomes an empty object.
func input(args []js.Value) []byte {
	if len(args) == 0 {
		return []byte("{}")
	}
	a := args[0]
	switch a.Type() {
	case js.TypeString:
		return []byte(a.String())
	case js.TypeUndefined, js.TypeNull:
		return []byte("{}")
	default:
		return []byte(js.Global().Get("JSON").Call("stringify", a).String())
	}
}

// toJS marshals a Go value and parses it back into a native JS object so the page
// receives structured data rather than a string to parse itself.
func toJS(v any) js.Value {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"error": err.Error(), "code": "marshal"})
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// --- error contract ---------------------------------------------------------

// errObj renders an error as the page's {error, code} shape.
func errObj(err error) map[string]any {
	return map[string]any{"error": err.Error(), "code": errCode(err)}
}

// argErr is a bridge-minted validation error (an unknown session, a bad enum)
// distinct from a library sentinel. It carries a stable code.
type argErr struct {
	code string
	msg  string
}

func (e *argErr) Error() string { return e.msg }

func badArg(code, msg string) error { return &argErr{code: code, msg: msg} }

// errCode maps an error to a stable string code: a bridge argErr keeps its own
// code, and every assent sentinel is mirrored onto its name. The page keys its
// teaching messages off these codes. An unrecognised error is "error".
func errCode(err error) string {
	var a *argErr
	if errors.As(err, &a) {
		return a.code
	}
	switch {
	case errors.Is(err, assent.ErrMakerChecker):
		return "maker_checker"
	case errors.Is(err, assent.ErrNoDelegation):
		return "no_delegation"
	case errors.Is(err, assent.ErrSeparation):
		return "separation"
	case errors.Is(err, assent.ErrNotApprover):
		return "not_approver"
	case errors.Is(err, assent.ErrStepNotActive):
		return "step_not_active"
	case errors.Is(err, assent.ErrNotPending):
		return "not_pending"
	case errors.Is(err, assent.ErrNotMaker):
		return "not_maker"
	case errors.Is(err, assent.ErrBadDecision):
		return "bad_decision"
	case errors.Is(err, assent.ErrBadEscalation):
		return "bad_escalation"
	case errors.Is(err, assent.ErrBadRule):
		return "bad_rule"
	case errors.Is(err, assent.ErrBadAssignment):
		return "bad_assignment"
	case errors.Is(err, assent.ErrEmptyStep):
		return "empty_step"
	case errors.Is(err, assent.ErrEmptyChain):
		return "empty_chain"
	case errors.Is(err, assent.ErrBadRequest):
		return "bad_request"
	case errors.Is(err, assent.ErrBadSnapshot):
		return "bad_snapshot"
	case errors.Is(err, assent.ErrExists):
		return "exists"
	case errors.Is(err, assent.ErrNotFound):
		return "not_found"
	default:
		return "error"
	}
}

// --- session state ----------------------------------------------------------

// sessionBase is the fixed instant every session's chain is submitted at: a
// Monday 09:00 in UTC, so the business schedule below opens on it and the SLA
// clock and delegation windows read cleanly against it.
var sessionBase = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// businessSchedule is the one calendar the playground runs its SLA clock on:
// Monday to Friday, 09:00 to 17:00 UTC, no holidays. It is the sundial.Schedule
// every escalation uses and the calendar Tick advances working hours over.
func businessSchedule() sundial.Schedule {
	return sundial.Schedule{
		Loc:  time.UTC,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}
}

// session is one live approval engine and everything the page drives it with.
// The engine owns the store and audit log internally; the bridge keeps the
// Delegations resolver so addDelegation can register a grant after construction,
// the request id, the shared schedule so Tick can advance working hours, and a
// virtual clock that decisions default their timestamp to.
type session struct {
	id     string
	eng    *assent.Engine
	deleg  *assent.Delegations
	reqID  string
	sched  sundial.Schedule
	clock  time.Time
	sod    string
	grants []assent.Delegation
}

var (
	mu       sync.Mutex
	sessions = map[string]*session{}
	seq      int
)

// getSession looks up a live session by id under the lock.
func getSession(id string) (*session, error) {
	mu.Lock()
	defer mu.Unlock()
	s, ok := sessions[id]
	if !ok {
		return nil, badArg("unknown_session", fmt.Sprintf("no live session %q; submit a chain first, or reload", id))
	}
	return s, nil
}

// --- request-building DTOs --------------------------------------------------

type actorDTO struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func (a actorDTO) toActor() assent.Actor { return assent.Actor{ID: a.ID, Name: a.Name} }

type assignmentDTO struct {
	User  string `json:"user,omitempty"`
	Group string `json:"group,omitempty"`
}

func (a assignmentDTO) toAssignment() (assent.Assignment, error) {
	if (a.User == "") == (a.Group == "") {
		return assent.Assignment{}, badArg("bad_assignment",
			"an approver names exactly one of a user or a group")
	}
	return assent.Assignment{User: a.User, Group: a.Group}, nil
}

type ruleDTO struct {
	Kind string `json:"kind"`
	N    int    `json:"n"`
}

func (r ruleDTO) toRule() (assent.Rule, error) {
	switch r.Kind {
	case "", "anyone":
		return assent.Anyone(), nil
	case "quorum":
		return assent.Quorum(r.N), nil
	case "unanimous":
		return assent.Unanimous(), nil
	default:
		return assent.Rule{}, badArg("invalid_argument",
			fmt.Sprintf("rule.kind must be anyone, quorum or unanimous; got %q", r.Kind))
	}
}

type levelDTO struct {
	Name    string  `json:"name"`
	AtHours float64 `json:"atHours"`
}

type escalationDTO struct {
	BudgetHours float64       `json:"budgetHours"`
	Levels      []levelDTO    `json:"levels"`
	Action      string        `json:"action"`
	Fallback    assignmentDTO `json:"fallback"`
}

func parseAction(s string) (assent.EscalationAction, error) {
	switch s {
	case "", "escalate_up":
		return assent.EscalateUp, nil
	case "reassign":
		return assent.Reassign, nil
	case "auto_approve":
		return assent.AutoApprove, nil
	default:
		return 0, badArg("invalid_argument",
			fmt.Sprintf("escalation.action must be escalate_up, reassign or auto_approve; got %q", s))
	}
}

func (e escalationDTO) toEscalation(sched sundial.Schedule) (*assent.Escalation, error) {
	action, err := parseAction(e.Action)
	if err != nil {
		return nil, err
	}
	levels := make([]sundial.Level, 0, len(e.Levels))
	for _, l := range e.Levels {
		levels = append(levels, sundial.Level{Name: l.Name, At: hours(l.AtHours)})
	}
	esc := &assent.Escalation{
		Schedule: sched,
		Budget:   hours(e.BudgetHours),
		Levels:   levels,
		Action:   action,
	}
	if action != assent.AutoApprove {
		fb, ferr := e.Fallback.toAssignment()
		if ferr != nil {
			return nil, ferr
		}
		esc.Fallback = fb
	}
	return esc, nil
}

type stepDTO struct {
	Name       string          `json:"name"`
	Approvers  []assignmentDTO `json:"approvers"`
	Rule       ruleDTO         `json:"rule"`
	Escalation *escalationDTO  `json:"escalation"`
}

type delegationDTO struct {
	From   actorDTO `json:"from"`
	To     actorDTO `json:"to"`
	Series []string `json:"series"`
	Start  string   `json:"start"`
	End    string   `json:"end"`
}

func (d delegationDTO) toDelegation() (assent.Delegation, error) {
	start, err := parseTimeDefault("delegation.start", d.Start, sessionBase.Add(-24*time.Hour))
	if err != nil {
		return assent.Delegation{}, err
	}
	end, err := parseTimeDefault("delegation.end", d.End, sessionBase.Add(30*24*time.Hour))
	if err != nil {
		return assent.Delegation{}, err
	}
	return assent.Delegation{
		From:   d.From.toActor(),
		To:     d.To.toActor(),
		Window: assent.Interval{Start: start, End: end},
		Series: d.Series,
	}, nil
}

type sessionSpec struct {
	SessionID   string          `json:"sessionId"`
	Maker       actorDTO        `json:"maker"`
	Series      string          `json:"series"`
	Subject     string          `json:"subject"`
	SoD         string          `json:"sod"`
	Steps       []stepDTO       `json:"steps"`
	Delegations []delegationDTO `json:"delegations"`
}

func hours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

// --- newSession -------------------------------------------------------------

// handleNewSession builds a fresh engine, submits the chain the page describes,
// registers any seed delegations, and stores the live session. It returns the
// whole state so the page renders the freshly-pending request. A validation
// failure (an empty step, an unusable rule, a bad escalation) returns the house
// error object; the chain builder is client-side, so no live state is lost.
func handleNewSession(in []byte) (any, error) {
	var spec sessionSpec
	if err := json.Unmarshal(in, &spec); err != nil {
		return nil, err
	}
	if spec.Maker.ID == "" {
		return nil, badArg("invalid_argument", "a maker id is required")
	}
	if len(spec.Steps) == 0 {
		return nil, badArg("invalid_argument", "a chain needs at least one step")
	}

	sched := businessSchedule()
	chain := make([]assent.Step, 0, len(spec.Steps))
	for _, sd := range spec.Steps {
		rule, err := sd.Rule.toRule()
		if err != nil {
			return nil, err
		}
		approvers := make([]assent.Assignment, 0, len(sd.Approvers))
		for _, ad := range sd.Approvers {
			a, aerr := ad.toAssignment()
			if aerr != nil {
				return nil, aerr
			}
			approvers = append(approvers, a)
		}
		st := assent.Step{
			Name:      sd.Name,
			Approvers: approvers,
			Rule:      rule,
		}
		if sd.Escalation != nil {
			esc, eerr := sd.Escalation.toEscalation(sched)
			if eerr != nil {
				return nil, eerr
			}
			st.Escalation = esc
		}
		chain = append(chain, st)
	}

	deleg := assent.NewDelegations()
	opts := []assent.Option{assent.WithDelegations(deleg)}
	if spec.SoD == "separate_steps" {
		opts = append(opts, assent.WithSoD(assent.SeparateSteps))
	}
	eng, err := assent.New(assent.NewMemStore(), chronicleLog(), opts...)
	if err != nil {
		return nil, err
	}

	id := spec.SessionID
	mu.Lock()
	if id == "" {
		seq++
		id = fmt.Sprintf("s%d", seq)
	}
	mu.Unlock()

	reqID := "req-" + id
	req := assent.Request{
		ID:      reqID,
		Maker:   spec.Maker.toActor(),
		Series:  spec.Series,
		Subject: spec.Subject,
		Chain:   chain,
	}
	if err := eng.Submit(ctx, req, sessionBase); err != nil {
		return nil, err
	}

	s := &session{
		id:    id,
		eng:   eng,
		deleg: deleg,
		reqID: reqID,
		sched: sched,
		clock: sessionBase,
		sod:   spec.SoD,
	}
	for _, dd := range spec.Delegations {
		g, gerr := dd.toDelegation()
		if gerr != nil {
			return nil, gerr
		}
		deleg.Add(g)
		s.grants = append(s.grants, g)
	}

	mu.Lock()
	sessions[id] = s
	mu.Unlock()

	return stateOf(s, nil)
}

// --- drive: decide / withdraw / reassign / tick -----------------------------

type decideReq struct {
	SessionID  string    `json:"sessionId"`
	Actor      actorDTO  `json:"actor"`
	OnBehalfOf *actorDTO `json:"onBehalfOf"`
	Group      string    `json:"group"`
	Approve    bool      `json:"approve"`
	Reason     string    `json:"reason"`
	At         string    `json:"at"`
}

// handleDecide records one ruling on the live request's current step. A rejection
// the engine issues — four-eyes, no delegation, not an approver — is embedded in
// the returned state, not raised as a bridge error, so the page shows the reason
// beside the unchanged chain. Maps to assent.Engine.Decide.
func handleDecide(in []byte) (any, error) {
	var req decideReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	at, err := parseTimeDefault("at", req.At, s.clock)
	if err != nil {
		return nil, err
	}
	d := assent.Decision{
		Actor:   req.Actor.toActor(),
		Group:   req.Group,
		Approve: req.Approve,
		Reason:  req.Reason,
		At:      at,
	}
	if req.OnBehalfOf != nil {
		a := req.OnBehalfOf.toActor()
		d.OnBehalfOf = &a
	}
	return stateOf(s, s.eng.Decide(ctx, s.reqID, d))
}

type withdrawReq struct {
	SessionID string   `json:"sessionId"`
	By        actorDTO `json:"by"`
	Reason    string   `json:"reason"`
	At        string   `json:"at"`
}

// handleWithdraw moves a pending request to Withdrawn on the maker's word. Maps
// to assent.Engine.Withdraw.
func handleWithdraw(in []byte) (any, error) {
	var req withdrawReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	at, err := parseTimeDefault("at", req.At, s.clock)
	if err != nil {
		return nil, err
	}
	return stateOf(s, s.eng.Withdraw(ctx, s.reqID, req.By.toActor(), req.Reason, at))
}

type reassignReq struct {
	SessionID string        `json:"sessionId"`
	From      assignmentDTO `json:"from"`
	To        assignmentDTO `json:"to"`
	By        actorDTO      `json:"by"`
	Reason    string        `json:"reason"`
	At        string        `json:"at"`
}

// handleReassign moves the active step's assignment to another approver. Maps to
// assent.Engine.Reassign. An empty from is a zero assignment, which the engine
// reads as "add to" rather than "replace".
func handleReassign(in []byte) (any, error) {
	var req reassignReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	at, err := parseTimeDefault("at", req.At, s.clock)
	if err != nil {
		return nil, err
	}
	to, err := req.To.toAssignment()
	if err != nil {
		return nil, err
	}
	// from may be a zero assignment (add rather than replace); do not validate it.
	from := assent.Assignment{User: req.From.User, Group: req.From.Group}
	return stateOf(s, s.eng.Reassign(ctx, s.reqID, from, to, req.By.toActor(), req.Reason, at))
}

type tickReq struct {
	SessionID string  `json:"sessionId"`
	Hours     float64 `json:"hours"`
}

// handleTick advances the session's virtual clock by the given working hours over
// the business schedule and polls the SLA. sundial.Schedule.Add computes the
// wall-clock instant that many working hours ahead — nights and weekends do not
// count — and assent.Engine.Tick applies any warn or breach that has come due.
// Ticking again is a no-op past the breach: the escalation is write-once.
func handleTick(in []byte) (any, error) {
	var req tickReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if req.Hours <= 0 {
		req.Hours = 1
	}
	target := s.sched.Add(s.clock, hours(req.Hours))
	terr := s.eng.Tick(ctx, s.reqID, target)
	if terr == nil {
		s.clock = target
	}
	return stateOf(s, terr)
}

type addDelegReq struct {
	SessionID string   `json:"sessionId"`
	From      actorDTO `json:"from"`
	To        actorDTO `json:"to"`
	Series    []string `json:"series"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
}

// handleAddDelegation registers a grant on the live session's resolver. A grant
// lets To act on behalf of From, inside the half-open window and for the listed
// series (empty means every series). Maps to assent.Delegations.Add.
func handleAddDelegation(in []byte) (any, error) {
	var req addDelegReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	if req.From.ID == "" || req.To.ID == "" {
		return nil, badArg("invalid_argument", "a delegation needs both a from and a to actor")
	}
	g, err := delegationDTO{From: req.From, To: req.To, Series: req.Series, Start: req.Start, End: req.End}.toDelegation()
	if err != nil {
		return nil, err
	}
	s.deleg.Add(g)
	s.grants = append(s.grants, g)
	return stateOf(s, nil)
}

type stateReq struct {
	SessionID string `json:"sessionId"`
}

// handleState re-reads the live session and returns its whole state and trail,
// for a plain re-render.
func handleState(in []byte) (any, error) {
	var req stateReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	s, err := getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	return stateOf(s, nil)
}

// --- time helpers -----------------------------------------------------------

// parseTimeDefault parses an RFC 3339 or plain-date string, returning def for an
// empty string. An unparseable value is a bridge argument error.
func parseTimeDefault(field, value string, def time.Time) (time.Time, error) {
	if value == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04", value); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, badArg("invalid_argument",
		fmt.Sprintf("%s must be an RFC 3339 timestamp such as 2026-01-05T09:00:00Z, got %q", field, value))
}
