/* assent playground — vanilla JS front end over the WebAssembly engine.
 *
 * The engine exposes a global `assent` object (see cmd/assent-wasm). assent is
 * stateful, so the page holds one live session id and re-renders from the whole
 * state value every call returns. Two error shapes come back: a malformed call
 * returns {error, code} with no state (a hard error); an engine rejection of a
 * well-formed decision — four-eyes, no delegation, not an approver — rides back
 * inside the state as {error, code} beside the unchanged chain, so the page
 * shows it as a teaching moment without losing the request.
 *
 * There is no network and no framework. Reload to reset.
 */

(function () {
  "use strict";

  let C = null; // the assent engine (window.assent)
  const SID = "main"; // one live session drives the whole page

  const APP = {
    st: null, // last full state from the engine
    builder: null, // client-side chain being edited (pre-submit)
    scenario: null,
  };

  // A stable default chain, used before any scenario loads.
  function defaultBuilder() {
    return {
      maker: "alice",
      series: "expense",
      subject: "expense #4021 — $4,000",
      sod: "none",
      steps: [
        { name: "Manager review", ruleKind: "anyone", ruleN: 2, approvers: [{ user: "mgr" }], esc: null },
        { name: "Finance sign-off", ruleKind: "anyone", ruleN: 2, approvers: [{ user: "finance" }], esc: null },
      ],
      delegations: [],
    };
  }

  // ---- tiny helpers --------------------------------------------------------

  function $(id) { return document.getElementById(id); }
  function ce(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function initials(s) { return String(s || "?").trim().charAt(0).toUpperCase() || "?"; }

  function fmtAt(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    if (isNaN(d)) return iso;
    return d.toLocaleString("en-US", {
      weekday: "short", month: "short", day: "numeric",
      hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "UTC",
    }) + " UTC";
  }

  // The engine's fixed submit instant, mirrored for the UI's date defaults.
  const BASE_MS = Date.UTC(2026, 0, 5, 9, 0, 0); // Mon 2026-01-05 09:00
  function dtLocal(ms) {
    const d = new Date(ms);
    const p = (n) => String(n).padStart(2, "0");
    return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())}T${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
  }

  const CODE_MSG = {
    maker_checker: "Four-eyes: the maker cannot be a checker (ErrMakerChecker). This bars the physical person — even holding another approver's delegated authority does not get the maker in.",
    no_delegation: "No delegation in force (ErrNoDelegation): the on-behalf-of authority has no grant covering this series at this decision time.",
    separation: "Segregation of duties (ErrSeparation): this authority already approved another step of the same request.",
    not_approver: "Not an approver (ErrNotApprover): this actor is not named on the chain, and holds no delegation for a named approver.",
    step_not_active: "Step not active (ErrStepNotActive): an earlier step must complete before this one takes decisions.",
    not_pending: "Not pending (ErrNotPending): a terminal request — approved, rejected or withdrawn — takes no more decisions.",
    not_maker: "Only the maker may withdraw (ErrNotMaker).",
    bad_decision: "Invalid decision (ErrBadDecision): an actor and a timestamp are required.",
    bad_assignment: "Invalid assignment (ErrBadAssignment): name exactly one of a user or a group.",
    bad_rule: "Invalid rule (ErrBadRule): a quorum must be at least one and no larger than the named approvers.",
    empty_step: "Empty step (ErrEmptyStep): a step needs at least one approver.",
    empty_chain: "Empty chain (ErrEmptyChain): a request needs at least one step.",
    bad_escalation: "Invalid escalation (ErrBadEscalation): check the schedule, budget and fallback approver.",
    unknown_session: "No live session — submit a chain first.",
  };
  function friendly(resp) { return (resp && (CODE_MSG[resp.code] || resp.error)) || "Something went wrong."; }
  function isHardError(resp) { return resp && resp.error && !Array.isArray(resp.steps); }

  function showBanner(id, text, kind) {
    const b = $(id);
    if (!text) { b.hidden = true; return; }
    b.textContent = text;
    b.className = (kind === "ok" ? "teach-banner ok" : b.classList.contains("err-banner") ? "err-banner" : "teach-banner");
    b.hidden = false;
  }
  function hide(id) { const b = $(id); if (b) b.hidden = true; }

  // ---- spec <-> builder conversion ----------------------------------------

  function builderToSpec() {
    const b = APP.builder;
    return {
      sessionId: SID,
      maker: { id: b.maker.trim() },
      series: b.series.trim(),
      subject: b.subject.trim(),
      sod: b.sod,
      steps: b.steps.map((s) => ({
        name: s.name,
        rule: { kind: s.ruleKind, n: Number(s.ruleN) || 0 },
        approvers: s.approvers.map((a) => (a.user ? { user: a.user } : { group: a.group })),
        escalation: s.esc
          ? {
              budgetHours: Number(s.esc.budgetHours) || 8,
              levels: Number(s.esc.warnHours) > 0 ? [{ name: "warn", atHours: Number(s.esc.warnHours) }] : [],
              action: s.esc.action,
              fallback: { user: s.esc.fallbackUser || "director" },
            }
          : null,
      })),
      delegations: b.delegations,
    };
  }

  function specToBuilder(spec) {
    return {
      maker: (spec.maker && spec.maker.id) || "alice",
      series: spec.series || "",
      subject: spec.subject || "",
      sod: spec.sod || "none",
      steps: (spec.steps || []).map((s) => ({
        name: s.name || "",
        ruleKind: (s.rule && s.rule.kind) || "anyone",
        ruleN: (s.rule && s.rule.n) || 2,
        approvers: (s.approvers || []).map((a) => (a.user ? { user: a.user } : { group: a.group })),
        esc: s.escalation
          ? {
              budgetHours: s.escalation.budgetHours,
              warnHours: s.escalation.levels && s.escalation.levels[0] ? s.escalation.levels[0].atHours : 0,
              action: s.escalation.action || "escalate_up",
              fallbackUser: (s.escalation.fallback && s.escalation.fallback.user) || "director",
            }
          : null,
      })),
      delegations: (spec.delegations || []).map((d) => ({
        from: { id: d.from.id, name: d.from.name },
        to: { id: d.to.id, name: d.to.name },
        series: d.series || [],
        start: d.start || "",
        end: d.end || "",
      })),
    };
  }

  // =========================================================================
  //  BUILDER (client-side chain editor)
  // =========================================================================

  function renderBuilder() {
    const b = APP.builder;
    $("b-maker").value = b.maker;
    $("b-series").value = b.series;
    $("b-subject").value = b.subject;
    $("b-sod").value = b.sod;

    const host = $("steps-editor");
    clear(host);
    b.steps.forEach((s, i) => host.appendChild(stepEditor(s, i)));
  }

  function stepEditor(s, i) {
    const card = ce("div", "step-edit");

    const head = ce("div", "step-edit-head");
    head.appendChild(ce("span", "sx", "S" + (i + 1)));
    const nm = ce("input", "stepname");
    nm.value = s.name; nm.placeholder = "step name";
    nm.addEventListener("input", () => { s.name = nm.value; });
    head.appendChild(nm);
    if (APP.builder.steps.length > 1) {
      const del = ce("button", "del-step", "×");
      del.type = "button"; del.title = "Remove step";
      del.addEventListener("click", () => { APP.builder.steps.splice(i, 1); renderBuilder(); });
      head.appendChild(del);
    }
    card.appendChild(head);

    // rule
    const rr = ce("div", "rule-row");
    rr.appendChild(ce("span", null, "rule"));
    const sel = ce("select");
    [["anyone", "Anyone (1 of N)"], ["quorum", "Quorum (M of N)"], ["unanimous", "Unanimous (all)"]].forEach(([v, t]) => {
      const o = ce("option", null, t); o.value = v; if (v === s.ruleKind) o.selected = true; sel.appendChild(o);
    });
    sel.addEventListener("change", () => { s.ruleKind = sel.value; renderBuilder(); });
    rr.appendChild(sel);
    if (s.ruleKind === "quorum") {
      const qn = ce("input", "qn"); qn.type = "number"; qn.min = "1"; qn.value = String(s.ruleN);
      qn.addEventListener("input", () => { s.ruleN = qn.value; });
      rr.appendChild(qn);
    }
    card.appendChild(rr);

    // approver chips
    const chips = ce("div", "chip-row");
    s.approvers.forEach((a, j) => {
      const chip = ce("span", "mini-chip" + (a.group ? " grp" : ""));
      chip.appendChild(ce("span", null, a.user ? a.user : a.group + " (group)"));
      const x = ce("span", "x", "×");
      x.addEventListener("click", () => { s.approvers.splice(j, 1); renderBuilder(); });
      chip.appendChild(x);
      chips.appendChild(chip);
    });
    card.appendChild(chips);

    // approver add
    const add = ce("div", "approver-add");
    const kind = ce("select");
    [["user", "user"], ["group", "group"]].forEach(([v, t]) => { const o = ce("option", null, t); o.value = v; kind.appendChild(o); });
    const inp = ce("input"); inp.placeholder = "id, then Add";
    const btn = ce("button", "btn ghost small", "Add");
    btn.type = "button";
    const doAdd = () => {
      const v = inp.value.trim();
      if (!v) return;
      s.approvers.push(kind.value === "group" ? { group: v } : { user: v });
      renderBuilder();
    };
    btn.addEventListener("click", doAdd);
    inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); doAdd(); } });
    add.appendChild(kind); add.appendChild(inp); add.appendChild(btn);
    card.appendChild(add);

    // escalation toggle + fields
    const escWrap = ce("div", "esc-toggle");
    const lbl = ce("label", "check");
    const cb = ce("input"); cb.type = "checkbox"; cb.checked = !!s.esc;
    cb.addEventListener("change", () => {
      s.esc = cb.checked ? { budgetHours: 8, warnHours: 4, action: "escalate_up", fallbackUser: "director" } : null;
      renderBuilder();
    });
    lbl.appendChild(cb);
    lbl.appendChild(ce("span", null, " SLA escalation (sundial)"));
    escWrap.appendChild(lbl);
    if (s.esc) {
      const ef = ce("div", "esc-fields");
      const g = ce("div", "grid2");
      g.appendChild(numField("budget (working h)", s.esc.budgetHours, (v) => { s.esc.budgetHours = v; }));
      g.appendChild(numField("warn at (working h)", s.esc.warnHours, (v) => { s.esc.warnHours = v; }));
      ef.appendChild(g);
      const g2 = ce("div", "rule-row");
      g2.appendChild(ce("span", null, "on breach"));
      const asel = ce("select");
      [["escalate_up", "add fallback"], ["reassign", "reassign to fallback"], ["auto_approve", "auto-approve"]].forEach(([v, t]) => {
        const o = ce("option", null, t); o.value = v; if (v === s.esc.action) o.selected = true; asel.appendChild(o);
      });
      asel.addEventListener("change", () => { s.esc.action = asel.value; renderBuilder(); });
      g2.appendChild(asel);
      if (s.esc.action !== "auto_approve") {
        const fb = ce("input"); fb.value = s.esc.fallbackUser; fb.placeholder = "fallback user";
        fb.style.maxWidth = "8em";
        fb.addEventListener("input", () => { s.esc.fallbackUser = fb.value; });
        g2.appendChild(fb);
      }
      ef.appendChild(g2);
      escWrap.appendChild(ef);
    }
    card.appendChild(escWrap);

    return card;
  }

  function numField(label, val, on) {
    const f = ce("label", "field");
    f.appendChild(ce("span", null, label));
    const i = ce("input"); i.type = "number"; i.min = "1"; i.value = String(val);
    i.addEventListener("input", () => on(Number(i.value)));
    f.appendChild(i);
    return f;
  }

  function readBuilderFields() {
    const b = APP.builder;
    b.maker = $("b-maker").value;
    b.series = $("b-series").value;
    b.subject = $("b-subject").value;
    b.sod = $("b-sod").value;
  }

  // =========================================================================
  //  SUBMIT + DRIVE
  // =========================================================================

  function submitChain() {
    readBuilderFields();
    const resp = C.newSession(builderToSpec());
    if (isHardError(resp)) { showBanner("drive-err", friendly(resp)); return; }
    hide("drive-err");
    setState(resp);
  }

  function setState(st) {
    APP.st = st;
    renderBadge(st);
    renderChain(st);
    renderFourEyes(st);
    renderDelegation(st);
    renderSLA(st);
    renderTrail(st);
  }

  // drive helpers: each returns the fresh state and renders it.
  function drive(fn) {
    const resp = fn();
    if (isHardError(resp)) return resp; // caller shows it
    setState(resp);
    return resp;
  }

  function decide(actor, approve, opts) {
    opts = opts || {};
    const req = { sessionId: SID, actor: { id: actor }, approve: approve, reason: opts.reason || (approve ? "approved" : "rejected") };
    if (opts.onBehalfOf) req.onBehalfOf = { id: opts.onBehalfOf };
    if (opts.group) req.group = opts.group;
    if (opts.at) req.at = opts.at;
    return drive(() => C.decide(req));
  }

  // =========================================================================
  //  RENDER — state badge
  // =========================================================================

  function renderBadge(st) {
    const badge = $("state-badge");
    badge.textContent = st.state;
    badge.className = "state-badge " + st.state;
    const total = st.steps.length;
    let note;
    if (st.state === "pending") note = `fsmx machine · on step ${st.stepIndex + 1} of ${total}`;
    else if (st.state === "approved") note = "fsmx machine · terminal — every step approved";
    else if (st.state === "rejected") note = "fsmx machine · terminal — a rejection failed it";
    else if (st.state === "withdrawn") note = "fsmx machine · terminal — the maker withdrew";
    else note = "fsmx machine";
    $("badge-note").textContent = note;
    $("withdraw-btn").disabled = st.state !== "pending";
  }

  // =========================================================================
  //  RENDER — chain visualization
  // =========================================================================

  function ruleLabel(step) {
    const n = step.approvers.length;
    if (step.rule.kind === "anyone") return `anyone · 1 of ${n}`;
    if (step.rule.kind === "quorum") return `quorum · ${step.rule.n} of ${n}`;
    return `unanimous · all ${n}`;
  }

  function decFor(step, appr) {
    for (const d of step.decisions) {
      const auth = (d.authority && d.authority.id) || d.actor.id;
      if (appr.user && auth === appr.user) return d;
      if (appr.group && d.group === appr.group) return d;
    }
    return null;
  }

  function renderChain(st) {
    const host = $("chain");
    clear(host);
    st.steps.forEach((step) => host.appendChild(chainStep(st, step)));
    hide("drive-err");
    if (st.error) showBanner("drive-err", friendly(st));
  }

  function chainStep(st, step) {
    const card = ce("div", "chain-step " + step.status);

    const head = ce("div", "cs-head");
    head.appendChild(ce("span", "cs-idx", "S" + (step.index + 1)));
    head.appendChild(ce("span", "cs-name", step.name || "Step " + (step.index + 1)));
    head.appendChild(ce("span", "cs-rule", ruleLabel(step)));
    const statusTxt = { active: "active", complete: "complete", rejected: "rejected", pending: "waiting" }[step.status] || step.status;
    head.appendChild(ce("span", "cs-status " + step.status, statusTxt));
    card.appendChild(head);

    // quorum counter
    const q = ce("div", "quorum");
    const qh = ce("div", "quorum-head");
    const kindWord = step.rule.kind === "unanimous" ? "approved" : "approvals";
    qh.appendChild(ce("span", null, kindWord));
    const cnt = ce("b", null, `${step.quorumHave} / ${step.quorumNeeded}`);
    qh.appendChild(cnt);
    q.appendChild(qh);
    const track = ce("div", "quorum-track");
    const fill = ce("div", "quorum-fill");
    const pct = step.quorumNeeded > 0 ? Math.min(100, (step.quorumHave / step.quorumNeeded) * 100) : 0;
    fill.style.width = pct + "%";
    track.appendChild(fill);
    q.appendChild(track);
    card.appendChild(q);

    // approver chips
    const appr = ce("div", "approvers");
    const fallbackId = step.escalation && step.escalation.breached && step.escalation.action !== "auto_approve"
      ? (step.escalation.fallback.user || step.escalation.fallback.group) : null;
    step.approvers.forEach((a) => appr.appendChild(approverChip(st, step, a, fallbackId)));
    card.appendChild(appr);

    // recorded decisions
    if (step.decisions.length) {
      const decs = ce("div", "decisions");
      step.decisions.forEach((d) => decs.appendChild(decisionLine(d)));
      card.appendChild(decs);
    }
    return card;
  }

  function approverChip(st, step, a, fallbackId) {
    const id = a.user || a.group;
    const d = decFor(step, a);
    let cls = "appr";
    if (a.group) cls += " group";
    if (d && d.approve) cls += " approved";
    else if (d && !d.approve) cls += " rejected";
    if (fallbackId && id === fallbackId) cls += " fallback";
    const chip = ce("span", cls);

    if (fallbackId && id === fallbackId) chip.appendChild(ce("span", "badge-fallback", "SLA"));

    const who = ce("span", "who", a.group ? id + " ▾" : id);
    chip.appendChild(who);

    if (d) {
      chip.appendChild(ce("span", "mark " + (d.approve ? "ok" : "no"), d.approve ? "✓" : "✗"));
      return chip;
    }
    // undecided: on the active step, offer a vote
    if (step.status === "active" && st.state === "pending") {
      let claim = null;
      if (a.group) {
        claim = ce("input", "gclaim"); claim.placeholder = "actor id";
        chip.appendChild(claim);
      }
      const act = ce("span", "act");
      const yes = ce("button", "vote yes", "✓"); yes.type = "button"; yes.title = "Approve";
      const no = ce("button", "vote no", "✗"); no.type = "button"; no.title = "Reject";
      const actorId = () => (a.group ? (claim.value.trim() || "") : a.user);
      const opts = (approve) => {
        const o = { reason: approve ? "approved" : "rejected" };
        if (a.group) o.group = a.group;
        return o;
      };
      yes.addEventListener("click", () => { const ai = actorId(); if (!ai) { claim.focus(); return; } decide(ai, true, opts(true)); });
      no.addEventListener("click", () => { const ai = actorId(); if (!ai) { claim.focus(); return; } decide(ai, false, opts(false)); });
      act.appendChild(yes); act.appendChild(no);
      chip.appendChild(act);
    }
    return chip;
  }

  function decisionLine(d) {
    const line = ce("div", "dec-line");
    line.appendChild(ce("span", "dmark " + (d.approve ? "yes" : "no"), d.approve ? "✓" : "✗"));
    const who = ce("span", "who", d.actor.id);
    line.appendChild(who);
    if (d.authority && d.authority.id !== d.actor.id) {
      line.appendChild(ce("span", "prov", "as " + d.authority.id));
    }
    if (d.group) line.appendChild(ce("span", "prov", "· via " + d.group));
    if (d.reason) line.appendChild(ce("span", "reason", "“" + d.reason + "”"));
    return line;
  }

  // =========================================================================
  //  RENDER — four-eyes / SoD
  // =========================================================================

  function renderFourEyes(st) {
    $("fe-maker-avatar").textContent = initials(st.maker.id);
    $("fe-maker-name").textContent = st.maker.id;
    $("fe-sod").checked = st.sod === "separate_steps";
  }

  function firstUserApprover(st) {
    const step = st.steps[st.stepIndex];
    if (step) {
      const u = step.approvers.find((a) => a.user);
      if (u) return u.user;
    }
    for (const s of st.steps) { const u = s.approvers.find((a) => a.user); if (u) return u.user; }
    return null;
  }

  function wireFourEyes() {
    $("fe-as-maker").addEventListener("click", () => {
      const st = APP.st; if (!st) return;
      const resp = decide(st.maker.id, true, { reason: "approving my own request" });
      routeTeach("fe-result", resp);
    });
    $("fe-as-maker-deleg").addEventListener("click", () => {
      const st = APP.st; if (!st) return;
      const appr = firstUserApprover(st);
      if (!appr) { showBanner("fe-result", "No named user approver on the chain to borrow authority from."); return; }
      // Grant the maker the approver's authority for this series, covering now.
      C.addDelegation({
        sessionId: SID, from: { id: appr }, to: { id: st.maker.id },
        series: st.series ? [st.series] : [],
        start: dtLocal(BASE_MS - 24 * 3600 * 1000), end: dtLocal(BASE_MS + 30 * 24 * 3600 * 1000),
      });
      const resp = decide(st.maker.id, true, { onBehalfOf: appr, reason: "acting for " + appr });
      if (resp && resp.code === "maker_checker") {
        showBanner("fe-result",
          `Still blocked. A delegation now grants ${st.maker.id} ${appr}'s authority — but ${st.maker.id} is the maker, so four-eyes bars the physical person before delegation is even resolved (ErrMakerChecker). Borrowing authority does not close the gap; assent does.`);
      } else {
        routeTeach("fe-result", resp);
      }
    });
    $("fe-sod").addEventListener("change", () => {
      APP.builder.sod = $("fe-sod").checked ? "separate_steps" : "none";
      $("b-sod").value = APP.builder.sod;
      submitChain();
      showBanner("fe-result",
        APP.builder.sod === "separate_steps"
          ? "SeparateSteps is on and the chain restarted. Now one person approving two steps of this request is blocked with ErrSeparation."
          : "Back to maker-checker only. The chain restarted.", "ok");
    });
  }

  function routeTeach(id, resp) {
    if (!resp) return;
    if (resp.error) showBanner(id, friendly(resp));
    else showBanner(id, "The decision was accepted.", "ok");
  }

  // =========================================================================
  //  RENDER — delegation
  // =========================================================================

  let delegPrefilled = false;
  function renderDelegation(st) {
    if (!delegPrefilled) {
      $("d-start").value = dtLocal(BASE_MS);
      $("d-end").value = dtLocal(BASE_MS + 4 * 24 * 3600 * 1000); // Fri
      delegPrefilled = true;
    }
    const host = $("deleg-list");
    clear(host);
    if (!st.delegations.length) {
      host.appendChild(ce("div", "deleg-empty", "No delegations yet. Add one, then approve on behalf of the authority."));
    } else {
      st.delegations.forEach((g) => {
        const it = ce("div", "deleg-item");
        it.appendChild(ce("span", "who", g.to.id));
        it.appendChild(ce("span", "arrow", "acts for"));
        it.appendChild(ce("span", "who", g.from.id));
        const scope = g.series && g.series.length ? g.series.join(", ") : "all series";
        it.appendChild(ce("span", null, "· " + scope));
        it.appendChild(ce("span", "win", fmtAt(g.start) + " → " + fmtAt(g.end)));
        host.appendChild(it);
      });
    }
  }

  function wireDelegation() {
    $("d-add").addEventListener("click", () => {
      const from = $("d-from").value.trim(), to = $("d-to").value.trim();
      if (!from || !to) { showBanner("deleg-err", "Both a from and a to actor are required."); return; }
      const seriesRaw = $("d-series").value.trim();
      const series = seriesRaw ? seriesRaw.split(",").map((s) => s.trim()).filter(Boolean) : [];
      const grant = { sessionId: SID, from: { id: from }, to: { id: to }, series, start: $("d-start").value, end: $("d-end").value };
      const resp = C.addDelegation(grant);
      if (isHardError(resp)) { showBanner("deleg-err", friendly(resp)); return; }
      // Persist into the builder so a rebuild keeps the grant.
      APP.builder.delegations.push({ from: { id: from }, to: { id: to }, series, start: $("d-start").value, end: $("d-end").value });
      hide("deleg-err");
      setState(resp);
    });
    $("ob-approve").addEventListener("click", () => {
      const actor = $("ob-actor").value.trim(), auth = $("ob-auth").value.trim();
      if (!actor || !auth) { showBanner("deleg-err", "A delegate and an authority are required."); return; }
      const resp = decide(actor, true, { onBehalfOf: auth, reason: $("ob-reason").value.trim() || "covering" });
      if (resp && !resp.error) showBanner("deleg-err", `Counted under ${auth}. The trail records “${actor}, as ${auth}”.`, "ok");
      else routeTeach("deleg-err", resp);
    });
    $("ob-outside").addEventListener("click", () => {
      const actor = $("ob-actor").value.trim(), auth = $("ob-auth").value.trim();
      if (!actor || !auth) { showBanner("deleg-err", "A delegate and an authority are required."); return; }
      // A decision timed well outside any weekly window.
      const outside = new Date(BASE_MS + 45 * 24 * 3600 * 1000).toISOString();
      const resp = decide(actor, true, { onBehalfOf: auth, reason: "outside the window", at: outside });
      routeTeach("deleg-err", resp);
    });
  }

  // =========================================================================
  //  RENDER — SLA escalation
  // =========================================================================

  // workingHours sums Mon–Fri 09:00–17:00 UTC working time between two instants,
  // for the SLA meter only. The authoritative warn/breach state comes from the
  // engine (sundial) via the trail; this is a smooth visual approximation.
  function workingHours(startMs, endMs) {
    if (endMs <= startMs) return 0;
    let total = 0;
    let d = new Date(startMs);
    let guard = 0;
    while (d.getTime() < endMs && guard < 400) {
      guard++;
      const y = d.getUTCFullYear(), m = d.getUTCMonth(), day = d.getUTCDate();
      const open = Date.UTC(y, m, day, 9, 0, 0), close = Date.UTC(y, m, day, 17, 0, 0);
      const dow = new Date(open).getUTCDay();
      if (dow >= 1 && dow <= 5) {
        const s = Math.max(startMs, open), e = Math.min(endMs, close);
        if (e > s) total += e - s;
      }
      d = new Date(Date.UTC(y, m, day + 1, 0, 0, 0));
    }
    return total / 3600000;
  }

  function renderSLA(st) {
    $("sla-clock").textContent = fmtAt(st.clock);
    const host = $("sla-body");
    clear(host);
    const withEsc = st.steps.filter((s) => s.escalation);
    if (!withEsc.length) {
      host.appendChild(ce("div", "sla-none", "No step on this chain carries an SLA. Add one in the builder (SLA escalation), or load the “SLA breach escalates” story."));
      return;
    }
    const elapsed = workingHours(BASE_MS, new Date(st.clock).getTime());
    withEsc.forEach((step) => host.appendChild(slaStep(st, step, elapsed)));
  }

  function slaStep(st, step, elapsedAll) {
    const e = step.escalation;
    const card = ce("div", "sla-step");
    const active = step.index === st.stepIndex && st.state === "pending";
    const title = ce("div", "sla-meter-head");
    title.appendChild(ce("span", null, `S${step.index + 1} ${step.name || ""} — ${e.budgetHours}h budget`));
    title.appendChild(ce("span", null, active ? "active" : (step.escalation.breached ? "breached" : "")));
    card.appendChild(title);

    // meter (only steps that have started count elapsed; approximate by step 0 entry)
    const elapsed = step.escalation.started ? Math.min(elapsedAll, e.budgetHours) : 0;
    const frac = e.budgetHours > 0 ? Math.min(1, (step.escalation.breached ? 1 : elapsed / e.budgetHours)) : 0;
    const track = ce("div", "sla-track");
    const fill = ce("div", "sla-fill" + (step.escalation.breached ? " breached" : ""));
    fill.style.width = (frac * 100).toFixed(1) + "%";
    track.appendChild(fill);
    card.appendChild(track);

    // marks for warn levels + budget
    const marks = ce("div", "sla-marks");
    (e.levels || []).forEach((l) => marks.appendChild(slaMark(l.atHours / e.budgetHours, l.name + " " + l.atHours + "h")));
    marks.appendChild(slaMark(1, "budget " + e.budgetHours + "h"));
    card.appendChild(marks);

    // facts / pills
    const facts = ce("div", "sla-facts");
    (e.levels || []).forEach((l) => {
      const fired = (step.escalation.fired || []).includes(l.name);
      facts.appendChild(ce("span", "sla-pill" + (fired ? " warn" : ""), (fired ? "⚠ " : "○ ") + l.name + " @ " + l.atHours + "h" + (fired ? " fired" : " pending")));
    });
    const actionText = { escalate_up: "add " + (e.fallback.user || e.fallback.group), reassign: "reassign → " + (e.fallback.user || e.fallback.group), auto_approve: "auto-approve" }[e.action] || e.action;
    facts.appendChild(ce("span", "sla-pill action", "on breach: " + actionText));
    if (step.escalation.breached) facts.appendChild(ce("span", "sla-pill breach", "✓ breached — action applied once"));
    card.appendChild(facts);
    return card;
  }

  function slaMark(frac, label) {
    const m = ce("div", "sla-mark");
    m.style.left = Math.min(100, Math.max(0, frac * 100)) + "%";
    m.appendChild(ce("span", "tick"));
    m.appendChild(document.createTextNode(label));
    return m;
  }

  function wireSLA() {
    $("sla-tick").addEventListener("click", () => {
      const hours = Number($("sla-hours").value) || 9;
      const resp = drive(() => C.tick({ sessionId: SID, hours }));
      if (isHardError(resp)) { showBanner("sla-err", friendly(resp)); return; }
      hide("sla-err");
    });
  }

  // =========================================================================
  //  RENDER — audit trail (chronicle)
  // =========================================================================

  const TRAIL_META = {
    submitted: { c: "var(--accent)", g: "▸", label: "Submitted" },
    decision: { c: "var(--ok)", g: "✓", label: "Decision" },
    advanced: { c: "var(--fsmx)", g: "→", label: "Advanced" },
    approved: { c: "var(--ok)", g: "✓", label: "Approved" },
    rejected: { c: "var(--no)", g: "✗", label: "Rejected" },
    withdrawn: { c: "var(--ink-faint)", g: "⨯", label: "Withdrawn" },
    warned: { c: "var(--warn)", g: "⚠", label: "SLA warn" },
    escalated: { c: "var(--sundial)", g: "↑", label: "Escalated" },
    auto_approved: { c: "var(--sundial)", g: "✓", label: "Auto-approved" },
    reassigned: { c: "var(--accent)", g: "⇄", label: "Reassigned" },
  };

  function renderTrail(st) {
    const host = $("trail");
    clear(host);
    if (!st.trail.length) { host.appendChild(ce("li", "trail-empty", "Nothing yet.")); return; }
    st.trail.forEach((en) => host.appendChild(trailLine(en)));
  }

  function trailLine(en) {
    const meta = TRAIL_META[en.type] || { c: "var(--ink-faint)", g: "•", label: en.type };
    const li = ce("li");
    const dot = ce("span", "tr-dot");
    dot.style.background = meta.c;
    dot.textContent = meta.g;
    li.appendChild(dot);

    const body = ce("div", "tr-body");
    let label = meta.label;
    if (en.type === "decision") label = en.approve ? "Approved step " + (en.step + 1) : "Rejected step " + (en.step + 1);
    body.appendChild(ce("span", "tr-type", label + " "));
    body.appendChild(ce("span", "who", en.actor.id));
    if (en.authority && en.authority.id !== en.actor.id) body.appendChild(ce("span", "prov", " as " + en.authority.id));
    const extra = en.extra || {};
    let tail = "";
    if (en.type === "advanced") tail = " → step " + (Number(extra.to) + 1);
    else if (en.type === "escalated") tail = " · " + (extra.action || "") + (extra.fallback ? " → " + extra.fallback : "");
    else if (en.type === "warned") tail = " · level " + (extra.level || "");
    else if (en.type === "reassigned") tail = " · " + (extra.from || "∅") + " → " + (extra.to || "");
    if (tail) body.appendChild(ce("span", null, tail));
    if (en.reason) body.appendChild(ce("span", "reason", " “" + en.reason + "”"));
    li.appendChild(body);

    li.appendChild(ce("span", "tr-at", fmtAt(en.at)));
    return li;
  }

  // =========================================================================
  //  SCENARIOS
  // =========================================================================

  function buildScenarioButtons() {
    const host = $("scenario-buttons");
    clear(host);
    (C.scenarios || []).forEach((sc) => {
      const b = ce("button", "btn", sc.title);
      b.type = "button"; b.dataset.name = sc.name;
      b.addEventListener("click", () => loadScenario(sc.name, b));
      host.appendChild(b);
    });
  }

  function loadScenario(name, btn) {
    const sc = C.loadScenario(name);
    if (!sc || sc.error) return;
    document.querySelectorAll("#scenario-buttons .btn").forEach((b) => b.classList.toggle("active", b === btn));
    const note = $("scenario-note");
    note.innerHTML = "";
    const strong = ce("b", null, sc.title + ". ");
    note.appendChild(strong);
    note.appendChild(document.createTextNode(sc.note));
    note.hidden = false;

    APP.scenario = name;
    APP.builder = specToBuilder(sc.spec);
    renderBuilder();
    submitChain();
    hide("fe-result"); hide("deleg-err"); hide("sla-err");

    // Prefill the on-behalf inputs for a delegation story.
    if (sc.spec.delegations && sc.spec.delegations.length) {
      const g = sc.spec.delegations[0];
      $("ob-actor").value = g.to.id; $("ob-auth").value = g.from.id;
      $("d-from").value = g.from.id; $("d-to").value = g.to.id;
      if (g.series && g.series.length) $("d-series").value = g.series.join(", ");
    }
    focusPanel(sc.focus);
  }

  function focusPanel(focus) {
    const map = { drive: "panel-drive", foureyes: "panel-foureyes", delegation: "panel-delegation", sla: "panel-sla" };
    const el = $(map[focus] || "panel-drive");
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  // =========================================================================
  //  WIRING + BOOT
  // =========================================================================

  function wire() {
    $("submit-btn").addEventListener("click", submitChain);
    $("add-step-btn").addEventListener("click", () => {
      APP.builder.steps.push({ name: "New step", ruleKind: "anyone", ruleN: 2, approvers: [{ user: "approver" }], esc: null });
      renderBuilder();
    });
    ["b-maker", "b-series", "b-subject"].forEach((id) => $(id).addEventListener("input", readBuilderFields));
    $("b-sod").addEventListener("change", () => { APP.builder.sod = $("b-sod").value; });
    $("withdraw-btn").addEventListener("click", () => {
      const st = APP.st; if (!st) return;
      const resp = drive(() => C.withdraw({ sessionId: SID, by: { id: st.maker.id }, reason: "no longer needed" }));
      if (resp && resp.error) showBanner("drive-err", friendly(resp));
    });
    wireFourEyes();
    wireDelegation();
    wireSLA();
  }

  let started = false;
  function start() {
    if (started) return;
    started = true;
    C = window.assent;

    APP.builder = defaultBuilder();
    buildScenarioButtons();
    renderBuilder();
    wire();

    $("boot").classList.add("hidden");

    // Open on the first story.
    const firstBtn = document.querySelector("#scenario-buttons .btn");
    if (firstBtn) loadScenario(firstBtn.dataset.name, firstBtn);
    else submitChain();
  }

  function boot() {
    if (!window.Go) { fail("wasm_exec.js failed to load."); return; }
    const go = new Go();
    const url = "assent.wasm";
    window.__assentOnReady = () => start();
    const inst = (obj) => { go.run(obj.instance); if (window.__assentReady) start(); };
    if (WebAssembly.instantiateStreaming) {
      WebAssembly.instantiateStreaming(fetch(url), go.importObject).then(inst).catch(() => fallback(go, url, inst));
    } else {
      fallback(go, url, inst);
    }
  }
  function fallback(go, url, inst) {
    fetch(url).then((r) => r.arrayBuffer()).then((buf) => WebAssembly.instantiate(buf, go.importObject)).then(inst)
      .catch((e) => fail("Could not load the wasm module: " + e));
  }
  function fail(msg) { const b = $("boot"); b.textContent = msg; b.classList.add("error"); }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
