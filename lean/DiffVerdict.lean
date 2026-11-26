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
    * a downgrade always recommends the old pin, with explicit empty-version fallbacks;
    * a true removal (no new address) never suppresses itself, duplicate shifts are
      idempotent, and a suppressed resource is never emitted again;
    * wrapper totals that fold in upstream findings preserve the INV-3 equivalence
      `total_breaking = 0 <=> can_safely_update` when upstream is not a downgrade.

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

/-! ## Pin fallbacks for empty version strings -/

theorem downgrade_without_old_version_has_no_pin (s : DiffSummary)
    (hDown : s.isDowngrade = true) (hOldEmpty : s.oldVersion = "") :
    safePin s = "" := by
  simp [safePin, hDown, hOldEmpty]

theorem breaking_without_new_version_uses_old_pin (s : DiffSummary)
    (hDown : s.isDowngrade = false) (hNewEmpty : s.newVersion = "")
    (hBreak : s.totalBreaking > 0) (hOld : s.oldVersion != "") :
    safePin s = s.oldVersion := by
  unfold safePin
  simp [hDown, hNewEmpty, hBreak, hOld, canSafelyUpdate]

theorem safe_without_new_version_has_no_pin (s : DiffSummary)
    (hDown : s.isDowngrade = false) (hBreak : s.totalBreaking = 0)
    (hNewEmpty : s.newVersion = "") :
    safePin s = "" := by
  unfold safePin
  simp [hDown, hBreak, hNewEmpty, canSafelyUpdate]

/-! ## Address-shift suppression -/

structure AddressShift where
  oldAddress : String
  newAddress : String
  isRecreation : Bool

/-- Mirrors the `shiftedOld` set built in `compareResources`: only a shift with a
    non-empty destination suppresses a finding at the original address. -/
def shiftedOld (shifts : List AddressShift) (addr : String) : Bool :=
  shifts.any (fun sh => sh.newAddress != "" && sh.oldAddress == addr)

/-- Old-address findings that survive suppression. -/
def emittedOldFindings (shifts : List AddressShift) (resources : List String) : List String :=
  resources.filter (fun addr => !shiftedOld shifts addr)

theorem true_removal_does_not_suppress (shifts : List AddressShift) (addr : String) :
    shiftedOld ({ oldAddress := addr, newAddress := "", isRecreation := true } :: shifts) addr =
      shiftedOld shifts addr := by
  simp [shiftedOld]

theorem duplicate_shifts_are_idempotent (shifts : List AddressShift) (addr : String) :
    shiftedOld (shifts ++ shifts) addr = shiftedOld shifts addr := by
  simp [shiftedOld]

theorem suppressed_resource_is_not_emitted (shifts : List AddressShift)
    (resources : List String) (addr : String) (h : shiftedOld shifts addr = true) :
    addr ∉ emittedOldFindings shifts resources := by
  simp [emittedOldFindings, h]

/-! ## Wrapper count equivalence (INV-3) -/

structure WrapperSummary where
  wrapperBreaking : Nat
  upstream : DiffSummary

/-- The wrapper verdict is unsafe when the wrapper itself has breaking issues or the
    folded upstream diff is unsafe, matching `AnalyzeWrapper` in `pkg/wrapper`. -/
def wrapperUnsafe (w : WrapperSummary) : Bool :=
  w.wrapperBreaking > 0 || !canSafelyUpdate w.upstream

/-- Upstream breaking counts are added to the wrapper total after verdict selection. -/
def wrapperTotalBreaking (w : WrapperSummary) : Nat :=
  w.wrapperBreaking + w.upstream.totalBreaking

theorem wrapper_total_breaking_zero_iff_safe (w : WrapperSummary)
    (hNoDowngrade : w.upstream.isDowngrade = false) :
    wrapperTotalBreaking w = 0 ↔ wrapperUnsafe w = false := by
  unfold wrapperUnsafe wrapperTotalBreaking canSafelyUpdate
  cases w.wrapperBreaking <;> cases w.upstream.totalBreaking <;> simp [hNoDowngrade]

end TerraformAstFormal
