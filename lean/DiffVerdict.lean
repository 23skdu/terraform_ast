/-
  Formal model of the safety decision procedure implemented by
  `pkg/diff.computeVerdict` and mirrored by wrapper reporting.

  The Go implementation classifies a module bump from three inputs: a downgrade
  flag, the number of breaking findings, and the number of warnings. This file
  proves the invariants that CI relies on:

    * safety is exactly "not a downgrade and no breaking findings";
    * the verdict partition (DOWNGRADE / BREAKING_CHANGES / WARNINGS_ONLY / SAFE)
      agrees with that safety decision;
    * introducing a breaking finding cannot leave a module marked safe;
    * a downgrade always recommends the old pin.

  These are proofs about the abstract decision procedure, not a proof that the
  Go parser or the repository's fidelity tests are correct. See
  `docs/formalproof.md` for scope and verification instructions.
-/
namespace TerraformAstFormal

inductive BumpType where
  | identical
  | patch
  | minor
  | major
  | downgrade
  | invalid
  deriving DecidableEq, Repr

inductive Verdict where
  | downgrade
  | breakingChanges
  | warningsOnly
  | safe
  deriving DecidableEq, Repr

structure DiffSummary where
  oldVersion : String
  newVersion : String
  bump : BumpType
  isDowngrade : Bool
  totalBreaking : Nat
  totalWarnings : Nat
  totalNewFeatures : Nat

/-- Mirrors `CanSafelyUpdate` in `computeVerdict`: warnings never block an update. -/
def canSafelyUpdate (s : DiffSummary) : Bool :=
  !(s.isDowngrade || s.totalBreaking > 0)

/-- Mirrors the ordered branches of `computeVerdict`. -/
def verdict (s : DiffSummary) : Verdict :=
  if s.isDowngrade then Verdict.downgrade
  else if s.totalBreaking > 0 then Verdict.breakingChanges
  else if s.totalWarnings > 0 then Verdict.warningsOnly
  else Verdict.safe

/-- Adding `k` breaking findings models a stricter diff of the same version pair. -/
def addBreaking (s : DiffSummary) (k : Nat) : DiffSummary :=
  { s with totalBreaking := s.totalBreaking + k }

/-- Mirrors the source of the recommended pin in `recommendSafePin`.
    An empty version string is represented by `""`, as in the Go report. -/
def safePin (s : DiffSummary) : String :=
  if s.isDowngrade then s.oldVersion
  else if canSafelyUpdate s && s.newVersion != "" then s.newVersion
  else if s.totalBreaking > 0 && s.oldVersion != "" then s.oldVersion
  else ""

theorem safe_iff_no_downgrade_no_breaking (s : DiffSummary) :
    canSafelyUpdate s = true ↔ s.isDowngrade = false ∧ s.totalBreaking = 0 := by
  unfold canSafelyUpdate
  cases s.isDowngrade <;> cases s.totalBreaking <;> simp

theorem verdict_eq_downgrade_iff (s : DiffSummary) :
    verdict s = Verdict.downgrade ↔ s.isDowngrade = true := by
  unfold verdict
  cases s.isDowngrade <;> cases s.totalBreaking <;> cases s.totalWarnings <;> simp

theorem verdict_eq_breaking_iff (s : DiffSummary) :
    verdict s = Verdict.breakingChanges ↔ s.isDowngrade = false ∧ s.totalBreaking > 0 := by
  unfold verdict
  cases s.isDowngrade <;> cases s.totalBreaking <;> cases s.totalWarnings <;> simp

theorem verdict_safe_iff_can_safely_update (s : DiffSummary) :
    verdict s = Verdict.safe ∨ verdict s = Verdict.warningsOnly ↔ canSafelyUpdate s = true := by
  rw [safe_iff_no_downgrade_no_breaking]
  unfold verdict
  cases s.isDowngrade <;> cases s.totalBreaking <;> cases s.totalWarnings <;> simp

theorem downgrade_keeps_old_pin (s : DiffSummary) (h : s.isDowngrade = true) :
    safePin s = s.oldVersion := by
  simp [safePin, h]

theorem added_breaking_is_unsafe (s : DiffSummary) (hDown : s.isDowngrade = false)
    (k : Nat) (hk : k > 0) :
    canSafelyUpdate (addBreaking s k) = false ∧
      verdict (addBreaking s k) = Verdict.breakingChanges := by
  have hPos : s.totalBreaking + k > 0 := by
    cases k with
    | zero => cases hk
    | succ k' => exact Nat.succ_pos (s.totalBreaking + k')
  constructor
  · unfold canSafelyUpdate addBreaking
    simp [hDown, hPos]
  · unfold verdict addBreaking
    simp [hDown, hPos]

end TerraformAstFormal
