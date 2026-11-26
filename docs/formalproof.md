# Formal Verification with Lean

`tfastdiff` decides whether an upstream bump is safe to pin from three inputs: the
downgrade flag, the breaking-change count, and the warning count. That decision is
business-critical in CI: an incorrect verdict can silently approve a state-destroying
upgrade. This document describes the machine-checked model of that decision procedure.

## Scope

| Item | Value |
| :--- | :--- |
| Proof language | Lean 4 (toolchain pinned to `4.36.0` in `lean/lean-toolchain`) |
| Source | `lean/DiffVerdict.lean` — the only Lean source file; all Lean files live under `lean/` |
| Go counterpart | `pkg/diff/diff.go` (`computeVerdict`, `recommendSafePin`) and `pkg/wrapper` reporting |
| Verification command | `make lean-verify` (does not require the Go toolchain) |
| Gate status | Opt-in: `make verify` remains runnable without Lean; CI wiring is a tracked roadmap step |

The proof covers the **abstract safety decision procedure**, not the HCL parser,
registry client, or the fidelity of AST comparison. Go tests, fuzzing, and the golden
plan oracle cover those parts. The Lean file is intentionally small enough that every
branch of the decision logic can be checked by case analysis rather than by trusting
an external solver library.

## Model

Lean mirrors the ordered branches of `computeVerdict`:

```lean
def canSafelyUpdate (s : DiffSummary) : Bool :=
  !(s.isDowngrade || s.totalBreaking > 0)

def verdict (s : DiffSummary) : Verdict :=
  if s.isDowngrade then Verdict.downgrade
  else if s.totalBreaking > 0 then Verdict.breakingChanges
  else if s.totalWarnings > 0 then Verdict.warningsOnly
  else Verdict.safe
```

`DiffSummary` keeps the fields that influence the verdict: version strings, bump type,
downgrade flag, and the breaking/warning/feature counters. `safePin` models the source
of the recommended pin; `addBreaking` models a stricter diff of the same version pair.

## Proven theorems

| Theorem | Statement | Why it matters |
| :--- | :--- | :--- |
| `safe_iff_no_downgrade_no_breaking` | `canSafelyUpdate s = true ↔ ¬s.isDowngrade ∧ s.totalBreaking = 0` | The safety claim used by `--exit-code` and policy enforcement is exact, not approximate. |
| `verdict_eq_downgrade_iff` | `verdict s = DOWNGRADE ↔ s.isDowngrade = true` | A downgrade can never be relabelled as a safe bump. |
| `verdict_eq_breaking_iff` | `verdict s = BREAKING_CHANGES ↔ ¬s.isDowngrade ∧ s.totalBreaking > 0` | Breaking findings are never downgraded to warnings. |
| `verdict_safe_iff_can_safely_update` | `SAFE ∨ WARNINGS_ONLY ↔ canSafelyUpdate s = true` | Report rendering and the exit code cannot disagree. |
| `downgrade_keeps_old_pin` | a downgrade always recommends `oldVersion` | Safe-pin advice cannot recommend a lower version. |
| `added_breaking_is_unsafe` | adding any positive number of breaking findings makes the verdict `BREAKING_CHANGES` and safety `false` | Safety is monotone: more findings cannot turn an unsafe diff safe. |

All proofs use core Lean only (`cases` plus `simp`); there is no Mathlib dependency,
so verification does not require a large package download.

## Verification workflow

1. Install [elan](https://github.com/leanprover/elan) so the `lean` shim is available.
2. Select the pinned toolchain (elan reads `lean/lean-toolchain` automatically inside
   this repository; the explicit command is useful after a fresh install):
   ```sh
   elan default "$(cat lean/lean-toolchain)"
   ```
3. Type-check the proof:
   ```sh
   make lean-verify
   ```
   This runs `lean lean/DiffVerdict.lean` and fails on any unsolved goal. It is
   deliberately separate from `make verify` so Go contributors without Lean are not
   blocked; roadmap item **L3** tracks adding it to CI.

Lean emits no `.olean` or build artifacts for this single-file check, so the Go
clean/build gates are unaffected.

## Keeping the proof honest

- When `computeVerdict`, `recommendSafePin`, or the safety semantics of `--exit-code`
  change, update `lean/DiffVerdict.lean` and `docs/formalproof.md` in the same commit.
- Run `make lean-verify` and `make verify` before publishing a release.
- Do not weaken a theorem to make a failing proof pass. If the Go behavior is
  intentionally different, change the model explicitly and document the divergence.
- New proofs belong under `lean/`; documentation about them belongs in this file and
  in the roadmap, never in an ad-hoc comment that can drift out of sync.
