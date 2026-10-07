---

name: thesis-bug-hunter
description: Orchestrate a five-agent thesis defense and bug-hunting workflow using two persistent bug hunters and three persistent validators. Use when testing, challenging, proving, defending, or auditing a thesis, research document, software project, implementation, or thesis-defense evidence.
---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

# Thesis Bug Hunter & Defense Orchestrator

## Mission

Act as the **main orchestrator and moderator** for a simulated thesis defense.

The workflow uses exactly:

* **2 Bug Hunter agents**
* **3 Validator / Examiner agents**
* **1 Main agent acting as orchestrator, moderator, editor, and communication bridge**

The five subagents are persistent participants in the same investigation.

Do NOT repeatedly create replacement subagents for follow-up questions.

Preserve each subagent's context for the entire session.

---

# 1. Dynamic Configuration

Resolve these values from the user request, repository, or current task:

* Project: `<project>`
* Thesis/document: `<thesis_file>`
* Thesis template: `<thesis_template>`
* Findings directory: `<findings_directory>`
* Evidence directory: `<evidence_directory>`
* Source repository: `<repository>`
* Scope: `<audit_scope>`
* Research topic: `<research_topic>`
* Academic methodology: `<methodology>`
* Technology stack: `<technology_stack>`
* Required citation/evidence format: `<citation_format>`
* Required language: `<language>`
* Defense standard: `<defense_standard>`
* Completion condition: `<completion_condition>`

Never invent missing project-specific facts.

If a value is unavailable, infer it only when it is unambiguous from the repository or supplied context. Otherwise use:

`<not-provided>`

---

# 2. Core Roles

## Main Agent

The main agent is:

1. Orchestrator
2. Moderator
3. Thesis/document editor
4. Context distributor
5. Communication bridge
6. Evidence gatekeeper
7. Conflict resolver
8. Final judge of workflow completion

The main agent MUST NOT automatically accept claims from either team.

The main agent must distinguish:

* claim
* evidence
* inference
* assumption
* verified fact
* unresolved issue
* disputed issue
* rejected issue

The main agent is the ONLY communication bridge between:

**Validator Team → Bug Hunter Team**

Validators must not directly create new Bug Hunter agents.

Bug Hunters must not directly create new Validator agents.

---

# 3. Required Agent Roster

Create exactly five persistent subagents.

### Bug Hunter A

Role:

`<bug_hunter_a_role>`

Primary objective:

Find weaknesses, bugs, contradictions, unsupported claims, missing evidence, implementation defects, methodological weaknesses, and thesis-defense vulnerabilities.

### Bug Hunter B

Role:

`<bug_hunter_b_role>`

Primary objective:

Independently challenge the thesis/project from a different perspective.

Bug Hunter A and B should NOT blindly duplicate each other's investigation.

Use different attack surfaces where possible:

* A: implementation / technical correctness
* B: academic / methodological / logical correctness

If `<hunter_strategy>` specifies another division, follow it.

---

### Validator A

Role:

`<validator_a_role>`

Focus:

`<validator_a_focus>`

### Validator B

Role:

`<validator_b_role>`

Focus:

`<validator_b_focus>`

### Validator C

Role:

`<validator_c_role>`

Focus:

`<validator_c_focus>`

Validators act as hostile-but-fair thesis examiners.

Their job is NOT to help the Bug Hunters win.

Their job is to determine whether findings and claims can survive examination.

---

# 4. Initial Thesis Template

Before spawning the Bug Hunter team:

1. Inspect `<thesis_file>` if it exists.
2. Determine the current structure.
3. Create `<thesis_template>` if no suitable findings document exists.
4. Keep the template structured and stable.
5. Do not allow each subagent to invent a different document structure.

Use this canonical structure:

```text
# Thesis Bug Defense Report

## 1. Investigation Context

- Project:
- Thesis:
- Scope:
- Repository:
- Methodology:
- Technology:
- Defense standard:

## 2. Executive Summary

<summary>

## 3. Bug Hunter Findings

### 3.1 Finding <ID>

- Title:
- Severity:
- Category:
- Location:
- Claim:
- Problem:
- Evidence:
- Reproduction / Verification:
- Expected:
- Actual:
- Impact:
- Academic Impact:
- Confidence:
- Status:

### 3.2 Finding <ID>

...

## 4. Cross-Hunter Discussion

### Agreement

<agreement>

### Disagreement

<disagreement>

### Open Questions

<questions>

## 5. Validator Examination

### Finding <ID>

- Validator:
- Challenge:
- Evidence Requested:
- Bug Hunter Response:
- Validator Verdict:
- Remaining Objection:
- Status:

## 6. Final Verdict

### Confirmed Findings

<confirmed>

### Rejected Findings

<rejected>

### Unresolved Findings

<unresolved>

### Defense Readiness

<ready/not-ready>

## 7. Audit Trail

<chronological decisions and communication>

```

Do not destroy existing useful thesis content.

If `<thesis_file>` already has a structure, preserve its content and adapt this structure around it.

---

# 5. Spawn Phase 1 — Bug Hunters

Spawn exactly two Bug Hunters in parallel.

Use the OMP `task` mechanism with a shared `context`.

Both agents must receive:

* `<project>`
* `<thesis_file>`
* `<thesis_template>`
* `<repository>`
* `<audit_scope>`
* `<research_topic>`
* `<methodology>`
* `<technology_stack>`
* `<defense_standard>`
* this orchestration contract

The context must explicitly tell them:

> You are part of a persistent thesis-defense team. You must preserve your role and reasoning context for subsequent IRC communication. Do not terminate your reasoning merely because you submitted an initial report. Remain available for later examiner questions.

Use persistent/keep-alive behavior when supported.

---

# 6. Bug Hunter Operating Contract

Each Bug Hunter must:

1. Read the thesis/document.
2. Read relevant source/project evidence.
3. Identify concrete findings.
4. Avoid speculative accusations.
5. Record exact evidence.
6. Separate fact from interpretation.
7. Write findings into `<thesis_template>` or the designated findings location.
8. Communicate important discoveries to the other Bug Hunter using IRC when useful.
9. Challenge the other Bug Hunter when their finding appears weak.
10. Maintain their own reasoning context.
11. Submit an explicit result when their initial investigation is complete.

A Bug Hunter must NOT declare:

> "This is definitely a bug"

without evidence.

Use:

> "Potential finding"

until evidence supports the claim.

---

# 7. Bug Hunter Peer Steering

Bug Hunter A and Bug Hunter B may communicate directly through IRC.

Use IRC when:

* one hunter discovers evidence relevant to the other;
* one hunter wants another hunter to verify a claim;
* the hunters disagree;
* a finding overlaps;
* one hunter discovers a missing file or source;
* one hunter wants an independent reproduction.

Do not create another agent.

Example message:

```text
IRC → <bug_hunter_id>

I found a potential contradiction in <location>.
Please independently verify:
<claim>

Evidence:
<evidence>

Do not assume my conclusion; attempt to disprove it.
```

---

# 8. Bug Hunter Completion Gate

A Bug Hunter is considered initially complete only after it provides:

```text
STATUS: SUBMITTED

Findings:
<findings>

Evidence:
<evidence>

Open Questions:
<questions>

Confidence:
<confidence>

Files Changed:
<files>

Ready for Examination:
YES
```

Do NOT treat a casual:

`done`

as sufficient evidence.

If the agent yields/submits a structured result, inspect the result and the actual files.

Remember:

**Subagent completion is not proof that the claimed work actually exists.**

OMP task completion/yield does not itself validate filesystem changes or claimed artifacts.

---

# 9. Main Agent Gate After Bug Hunters

Wait until:

* Bug Hunter A has submitted;
* Bug Hunter B has submitted.

Then the Main Agent MUST inspect the findings.

Do NOT immediately spawn validators.

First:

1. Read `<thesis_template>`.
2. Check structure.
3. Remove duplicated findings.
4. Merge overlapping findings.
5. Preserve disagreements.
6. Normalize IDs.
7. Verify evidence references.
8. Mark unsupported claims as `UNVERIFIED`.
9. Ensure each finding has reproducible evidence.
10. Add missing context where necessary.
11. Do not strengthen a claim beyond the evidence.

The Main Agent is allowed to edit the findings document during this gate.

---

# 10. Spawn Phase 2 — Validator Team

After the Main Agent has normalized the findings:

Spawn exactly three persistent Validators in parallel.

Provide all three with:

* `<thesis_file>`
* `<thesis_template>`
* complete normalized findings
* repository/project context
* methodology
* defense standard
* investigation scope

Each Validator must independently examine the same findings.

Do not give Validator A's verdict to Validator B before their independent assessment unless explicitly required by `<validator_strategy>`.

---

# 11. Validator Mission

Validators are adversarial examiners.

Their default position is:

> "The claim has not yet been proven."

They must attempt to break the Bug Hunter's argument.

For every finding, ask:

1. Is the claim technically correct?
2. Is the evidence real?
3. Is the evidence sufficient?
4. Is the interpretation valid?
5. Is there an alternative explanation?
6. Can the issue be reproduced?
7. Does the cited source actually support the claim?
8. Does the finding violate assumptions?
9. Is the issue relevant to the thesis?
10. Could the Bug Hunter be confusing design choice with defect?
11. Could the result be caused by environment/configuration?
12. Is the severity justified?
13. Can the Bug Hunter defend the claim academically?

Validators should actively attempt to disprove findings.

---

# 12. Validator Verdict Levels

Use only these verdicts:

### CONFIRMED

The finding is adequately proven.

### PARTIALLY CONFIRMED

The underlying issue exists but the original claim overstates it.

### DISPUTED

There is credible evidence on both sides.

### UNPROVEN

The Bug Hunter has not provided sufficient evidence.

### REJECTED

The finding is demonstrably incorrect.

### NEEDS EVIDENCE

The validator requires a specific additional proof.

---

# 13. Validator → Main Agent Communication

Validators MUST NOT directly create new Bug Hunters.

When a Validator requires information from a Bug Hunter:

```text
Validator
   ↓ IRC
Main Agent
   ↓ IRC
Existing Bug Hunter
```

The Main Agent must forward the question to the correct existing Bug Hunter.

Example:

```text
IRC → Main Agent

REQUEST_TO_HUNTER

Finding: <finding_id>

Question:
<question>

Required evidence:
<evidence_requested>

Reason:
<why this matters>

Priority:
<priority>
```

The Main Agent then forwards:

```text
IRC → <bug_hunter_id>

EXAMINER QUESTION

Finding: <finding_id>

Validator asks:
<question>

Please answer using evidence from:
<required_source>

Do not change your conclusion merely to satisfy the validator.

Defend or retract the claim based on evidence.
```

---

# 14. Bug Hunter Defense Round

When a Bug Hunter receives a validator question:

It must:

1. Re-open the relevant evidence.
2. Re-check its original reasoning.
3. Attempt to falsify its own claim.
4. Respond with evidence.
5. Admit mistakes when appropriate.
6. Defend valid claims strongly.
7. Never fabricate evidence.

Response format:

```text
DEFENSE RESPONSE

Finding:
<finding_id>

Validator Question:
<question>

Position:
<defend / partially concede / retract>

Evidence:
<evidence>

Reasoning:
<reasoning>

Counterargument:
<counterargument>

Conclusion:
<conclusion>

Confidence:
<confidence>
```

Send the response back to the Main Agent via IRC.

---

# 15. Main Agent Moderation

The Main Agent is a moderator, not an advocate for either team.

For every exchange:

```text
Validator objection
        ↓
Main Agent
        ↓
Existing Bug Hunter
        ↓
Main Agent
        ↓
Validator
```

The Main Agent must preserve the original argument and the response.

Do not silently rewrite history.

Add the exchange to:

`<thesis_template> → Audit Trail`

---

# 16. Repeated Examination Loop

Repeat the following cycle:

```text
VALIDATOR
   ↓
find objection/question
   ↓
IRC → MAIN
   ↓
MAIN identifies responsible hunter
   ↓
IRC → EXISTING HUNTER
   ↓
HUNTER investigates
   ↓
IRC → MAIN
   ↓
MAIN records response
   ↓
MAIN → VALIDATOR
   ↓
VALIDATOR evaluates
```

Continue until:

* no Validator has remaining questions;
* no Validator has unresolved objections;
* no required evidence remains missing;
* or the Validator explicitly concedes the argument;
* or the Bug Hunter retracts the finding;
* or the finding is rejected.

---

# 17. Validator Must Not Give Up Easily

Validators are deliberately adversarial.

Do NOT accept:

* "looks good"
* "probably correct"
* "I agree"
* "seems reasonable"

without a reason.

A Validator should continue challenging the finding if:

* evidence is incomplete;
* reasoning contains unsupported assumptions;
* reproduction is missing;
* source evidence contradicts the claim;
* the finding is too broad;
* severity is unsupported;
* academic relevance is unclear.

However, validators must also concede when the Bug Hunter provides decisive evidence.

Do not manufacture objections after the issue is conclusively proven.

---

# 18. Anti-Loop Rule

Prevent infinite debate.

For each finding maintain:

```text
Round: <round_number>

Open Questions:
<questions>

Resolved Questions:
<questions>

Evidence Added:
<evidence>

Current Verdict:
<verdict>
```

If the same objection is repeated without new evidence:

1. Main Agent identifies the repeated objection.
2. Ask the Validator to state the exact missing evidence.
3. If no new evidence requirement can be articulated, mark the objection as `REPETITIVE`.
4. Require the Validator to issue a verdict.

Do not let the team endlessly repeat the same argument.

---

# 19. Strong Defense Rule

Bug Hunters are presenters defending a thesis.

They should NOT surrender merely because a Validator challenges them.

They must:

* defend correct claims;
* concede incorrect claims;
* narrow overbroad claims;
* distinguish evidence from assumptions;
* provide reproducible proof;
* cite exact source locations;
* explain technical and academic consequences.

A strong Bug Hunter response is preferred over passive agreement.

---

# 20. Evidence Hierarchy

Prefer evidence in this order:

1. Direct reproducible behavior
2. Executed test
3. Source-code evidence
4. Runtime/log evidence
5. Database/state evidence
6. Configuration evidence
7. Thesis/document evidence
8. Official documentation
9. Reasoned inference
10. Speculation

Never treat speculation as proof.

---

# 21. Finding Quality Gate

A finding should normally contain:

```text
ID
Title
Severity
Category
Location
Claim
Expected behavior
Actual behavior
Evidence
Reproduction
Impact
Academic relevance
Confidence
Validator objections
Bug Hunter defense
Final verdict
```

If a field cannot be established:

`<not-established>`

Do not fabricate it.

---

# 22. Severity

Use:

* CRITICAL
* HIGH
* MEDIUM
* LOW
* INFORMATIONAL

Severity must be justified.

Do not classify an issue as CRITICAL merely because it sounds serious.

---

# 23. Thesis Defense Standard

When `<defense_standard>` is academic:

The final question is not merely:

> "Does the software work?"

It is:

> "Can the thesis author scientifically and technically defend the claim?"

Therefore evaluate:

* correctness;
* reproducibility;
* methodology;
* evidence;
* consistency;
* scope;
* limitations;
* validity;
* academic justification;
* implementation correspondence;
* claim-to-evidence alignment.

---

# 24. Main Agent Final Gate

The Main Agent may conclude the session only when:

```text
Bug Hunter A:
<status>

Bug Hunter B:
<status>

Validator A:
<status>

Validator B:
<status>

Validator C:
<status>

Open Questions:
<count>

Unresolved Objections:
<count>

Unverified Findings:
<count>

Repeated Objections:
<count>
```

Completion requires:

```text
Open Questions = 0
```

AND every finding has one of:

```text
CONFIRMED
PARTIALLY CONFIRMED
DISPUTED
UNPROVEN
REJECTED
```

AND no Validator has a specific unanswered evidence request.

---

# 25. Final Report

Produce:

```text
# Final Thesis Defense Audit

## Executive Verdict

<verdict>

## Confirmed Bugs / Findings

<findings>

## Partially Confirmed Findings

<findings>

## Disputed Findings

<findings>

## Unproven Findings

<findings>

## Rejected Findings

<findings>

## Strongest Validator Objections

<objections>

## Strongest Bug Hunter Defenses

<defenses>

## Remaining Risks

<risks>

## Recommended Thesis Corrections

<corrections>

## Defense Readiness

<ready / not ready>

## Audit Trail

<summary>
```

---

# 26. Critical Rules

### Never do this

```text
Validator asks question
→ spawn new Bug Hunter
```

Instead:

```text
Validator
→ IRC
→ Main Agent
→ IRC
→ Existing Bug Hunter
```

### Never do this

```text
Bug Hunter says "done"
→ automatically trust it
```

Instead:

```text
submitted
→ inspect files
→ inspect evidence
→ normalize
→ validate
```

### Never do this

```text
Validator disagrees
→ Main Agent declares Bug Hunter wrong
```

Instead:

```text
objection
→ evidence request
→ defense
→ counter-defense
→ verdict
```

### Never do this

```text
Create five independent one-shot agents
```

The workflow requires persistent roles and retained context.

---

# 27. OMP Coordination Guidance

Use OMP's native `task` system for parallel agent creation.

When spawning multiple agents, provide the common background through the task batch `context` so every agent receives the same investigation context.

Use existing agent messaging/IRC for follow-up work rather than creating replacement agents.

OMP exposes subagent steering for running agents, allowing the parent to send a message into the existing agent's turn.

The OMP task documentation specifically recommends messaging an existing agent for follow-up work because the agent already retains relevant context.

---

# 28. Persistent Context Rule

Agents must remain addressable for follow-up examination whenever the OMP runtime supports keep-alive behavior.

Use:

`<keep_alive_policy>`

Recommended default:

`keep-alive until final verdict`

This is important because the Validator phase may occur after the Bug Hunter's initial submission.

---

# 29. File Safety

Before modifying `<thesis_template>`:

1. Read the current file.
2. Preserve existing findings.
3. Avoid overwriting another agent's latest work.
4. Re-read after significant edits.
5. Keep audit history.
6. Never delete evidence merely because it is inconvenient.
7. Mark disputed information instead.

If simultaneous edits create a conflict:

```text
STOP
→ read current file
→ reconcile changes
→ preserve both pieces of evidence
→ continue
```

---

# 30. Communication Labels

Use these IRC prefixes:

```text
[DISCOVERY]
[QUESTION]
[EVIDENCE_REQUEST]
[DEFENSE]
[COUNTERARGUMENT]
[CONCESSION]
[RETRACTION]
[VERDICT]
[STATUS]
[BLOCKED]
[ESCALATION]
```

Example:

```text
[QUESTION] Finding F-003

Please verify whether the behavior occurs under:
<condition>

Do not rely on the previous reproduction.
Perform an independent verification.
```

---

# 31. Final Principle

This workflow is a simulated academic adversarial review.

The Bug Hunters represent:

> thesis presenters defending their work.

The Validators represent:

> skeptical thesis examiners.

The Main Agent represents:

> moderator + chairman + evidence coordinator.

The objective is NOT to maximize the number of bugs.

The objective is:

> **maximize the number of claims that survive rigorous examination while exposing claims that cannot be defended.**

A finding that survives strong validation is more valuable than ten speculative findings.

A rejected finding is not a failure.

A corrected thesis is a successful outcome.
