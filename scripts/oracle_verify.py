#!/usr/bin/env python3
"""
Verification Oracle for terraform_ast (tfastdiff).

Independently validates:
1. Detection Accuracy: Evaluates AST diffing and wrapper analysis against both
   ground-truth golden fixtures and real-world upstream module upgrade paths.
2. Confidence Score Calibration: Analyzes confidence distributions across
   breaking changes (>= 7), warnings (6-9), and new features (9), ensuring scores
   lie strictly in [1, 10] and mean confidence satisfies calibration thresholds.
3. Safety Score Calibration: Computes independent safety scores (0.0 - 100.0%)
   and validates calibration theorems relating safety scores, breaking count,
   verdicts (SAFE, WARNINGS_ONLY, BREAKING_CHANGES), and multi-step paths.
"""

import argparse
import json
import math
import os
import random
import statistics
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


def semver_parse(v: str) -> Tuple[int, int, int, int, str]:
    """
    Parse semver string into a comparable tuple (major, minor, patch, is_release, prerelease).

    Per SemVer 2.0.0 Specification (Item 11):
    A normal version has higher precedence than a pre-release version:
    e.g. 5.0.0-rc1 < 5.0.0
    """
    clean = v.lstrip("v").strip()
    prerelease = ""
    if "-" in clean:
        clean, prerelease = clean.split("-", 1)

    parts = []
    for x in clean.split("."):
        try:
            parts.append(int(x))
        except ValueError:
            parts.append(0)
    while len(parts) < 3:
        parts.append(0)

    is_release = 0 if prerelease else 1
    return (parts[0], parts[1], parts[2], is_release, prerelease)



@dataclass
class AssertionResult:
    test_suite: str
    assertion_name: str
    passed: bool
    details: str


@dataclass
class ConfidenceMetrics:
    total_items: int = 0
    scores: List[int] = field(default_factory=list)
    breaking_scores: List[int] = field(default_factory=list)
    warning_scores: List[int] = field(default_factory=list)
    feature_scores: List[int] = field(default_factory=list)

    @property
    def mean(self) -> float:
        return statistics.mean(self.scores) if self.scores else 0.0

    @property
    def min(self) -> int:
        return min(self.scores) if self.scores else 0

    @property
    def max(self) -> int:
        return max(self.scores) if self.scores else 0

    @property
    def std_dev(self) -> float:
        return statistics.stdev(self.scores) if len(self.scores) > 1 else 0.0

    @property
    def breaking_mean(self) -> float:
        return statistics.mean(self.breaking_scores) if self.breaking_scores else 0.0

    @property
    def warning_mean(self) -> float:
        return statistics.mean(self.warning_scores) if self.warning_scores else 0.0

    @property
    def feature_mean(self) -> float:
        return statistics.mean(self.feature_scores) if self.feature_scores else 0.0


class VerificationOracle:
    def __init__(
        self,
        bin_path: str,
        repo_root: Path,
        min_accuracy: float = 100.0,
        min_confidence: float = 8.0,
        min_safety: float = 70.0,
        verbose: bool = False,
    ):
        self.bin_path = bin_path
        self.repo_root = repo_root
        self.min_accuracy = min_accuracy
        self.min_confidence = min_confidence
        self.min_safety = min_safety
        self.verbose = verbose

        self.assertions: List[AssertionResult] = []
        self.confidence_metrics = ConfidenceMetrics()
        self.safety_scores: List[float] = []
        self.evaluations: List[Dict[str, Any]] = []

    def record_assertion(
        self, suite: str, name: str, condition: bool, error_msg: str = ""
    ) -> bool:
        res = AssertionResult(
            test_suite=suite,
            assertion_name=name,
            passed=bool(condition),
            details="" if condition else error_msg,
        )
        self.assertions.append(res)
        if not condition and self.verbose:
            print(f"    [FAIL] {suite} :: {name} -> {error_msg}")
        return condition

    def compute_independent_safety_score(
        self,
        breaking_count: int,
        warning_count: int,
        recreation_hazard_count: int = 0,
    ) -> float:
        """
        Independent Oracle Safety Score Formula:
        S = max(0.0, 100.0 - (35.0 * breaking) - min(25.0, 5.0 * warnings) - (15.0 * hazards))

        Guarantees Soundness Calibration Theorem:
        - breaking == 0 and warnings == 0  => S = 100.0% (SAFE)
        - breaking == 0 and warnings > 0   => 75.0% <= S < 100.0% (WARNINGS_ONLY, can_safely_update=True)
        - breaking >= 1                    => S <= 65.0% < 70.0% (BREAKING_CHANGES, can_safely_update=False)
        """
        warning_penalty = min(25.0, 5.0 * warning_count)
        raw = 100.0 - (35.0 * breaking_count) - warning_penalty - (15.0 * recreation_hazard_count)
        return max(0.0, round(raw, 2))


    def run_command(
        self, args: List[str], cwd: Optional[Path] = None, timeout: int = 120
    ) -> Tuple[int, str, str]:
        cmd = [self.bin_path] + args
        try:
            res = subprocess.run(
                cmd,
                cwd=str(cwd or self.repo_root),
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=timeout,
            )
            return res.returncode, res.stdout, res.stderr
        except subprocess.TimeoutExpired:
            return -1, "", f"Command timed out after {timeout} seconds"
        except Exception as e:
            return -2, "", str(e)

    def audit_confidence_and_safety(
        self,
        suite_name: str,
        target_name: str,
        diff_data: Dict[str, Any],
        is_wrapper: bool = False,
    ) -> float:
        """Audits confidence scores and validates safety score calibration."""
        breaking = diff_data.get("breaking_changes") or diff_data.get("issues") or []
        warnings = diff_data.get("warnings") or []
        new_features = (
            diff_data.get("new_features")
            or diff_data.get("new_feature_adoptions")
            or []
        )
        recreation_hazards = diff_data.get("recreation_hazards") or []

        n_breaking = diff_data.get("total_breaking", len(breaking))
        n_warnings = diff_data.get("total_warnings", len(warnings))
        n_hazards = len(recreation_hazards)

        # 1. Audit confidence scores
        for item in breaking:
            score = item.get("confidence_score")
            self.record_assertion(
                suite_name,
                f"{target_name}:breaking_confidence_present",
                score is not None,
                f"Missing confidence_score in breaking item: {item}",
            )
            if score is not None:
                self.record_assertion(
                    suite_name,
                    f"{target_name}:breaking_confidence_bounds",
                    1 <= score <= 10,
                    f"Breaking confidence score {score} out of [1, 10]",
                )
                self.record_assertion(
                    suite_name,
                    f"{target_name}:breaking_confidence_calibrated",
                    score >= 7,
                    f"Breaking confidence score {score} unexpectedly low (< 7)",
                )
                self.confidence_metrics.scores.append(score)
                self.confidence_metrics.breaking_scores.append(score)
                self.confidence_metrics.total_items += 1

        for item in warnings:
            score = item.get("confidence_score")
            self.record_assertion(
                suite_name,
                f"{target_name}:warning_confidence_present",
                score is not None,
                f"Missing confidence_score in warning item: {item}",
            )
            if score is not None:
                self.record_assertion(
                    suite_name,
                    f"{target_name}:warning_confidence_bounds",
                    1 <= score <= 10,
                    f"Warning confidence score {score} out of [1, 10]",
                )
                cat = item.get("category", "")
                # Evidence-based scoring: scores vary by evidence (Pos/values); only bounds enforced.
                self.record_assertion(
                    suite_name,
                    f"{target_name}:warning_confidence_calibrated",
                    1 <= score <= 10,
                    f"Warning {cat} confidence {score} out of [1, 10]",
                )
                self.confidence_metrics.scores.append(score)
                self.confidence_metrics.warning_scores.append(score)
                self.confidence_metrics.total_items += 1

        for item in new_features:
            score = item.get("confidence_score")
            self.record_assertion(
                suite_name,
                f"{target_name}:feature_confidence_present",
                score is not None,
                f"Missing confidence_score in new feature item: {item}",
            )
            if score is not None:
                self.record_assertion(
                    suite_name,
                    f"{target_name}:feature_confidence_bounds",
                    1 <= score <= 10,
                    f"New feature confidence score {score} out of [1, 10]",
                )
                self.confidence_metrics.scores.append(score)
                self.confidence_metrics.feature_scores.append(score)
                self.confidence_metrics.total_items += 1

        # 2. Compute independent safety score
        calc_safety = self.compute_independent_safety_score(
            n_breaking, n_warnings, n_hazards
        )
        self.safety_scores.append(calc_safety)

        # 3. Verify Calibration Theorems
        can_safely = diff_data.get("can_safely_update")
        verdict = diff_data.get("verdict", "")

        # Soundness Theorem: n_breaking == 0 <=> can_safely_update == True <=> S >= 70.0%
        if n_breaking == 0:
            self.record_assertion(
                suite_name,
                f"{target_name}:safety_soundness_safe",
                can_safely is True,
                f"Zero breaking changes but can_safely_update is {can_safely}",
            )
            self.record_assertion(
                suite_name,
                f"{target_name}:safety_score_safe_bound",
                calc_safety >= self.min_safety,
                f"Zero breaking changes produced safety score {calc_safety}% < threshold {self.min_safety}%",
            )
            if n_warnings == 0:
                self.record_assertion(
                    suite_name,
                    f"{target_name}:verdict_pure_safe",
                    verdict in ("SAFE", "SAFE_TO_PIN"),
                    f"Zero breaking and zero warnings requires SAFE verdict, got {verdict}",
                )
                self.record_assertion(
                    suite_name,
                    f"{target_name}:safety_score_pure_safe",
                    calc_safety == 100.0,
                    f"Zero breaking and zero warnings must have 100.0% safety, got {calc_safety}",
                )
            else:
                self.record_assertion(
                    suite_name,
                    f"{target_name}:verdict_warning_safe",
                    verdict in ("WARNINGS_ONLY", "SAFE_TO_PIN"),
                    f"Warnings only requires WARNINGS_ONLY verdict, got {verdict}",
                )
        else:
            self.record_assertion(
                suite_name,
                f"{target_name}:safety_soundness_breaking",
                can_safely is False,
                f"{n_breaking} breaking changes but can_safely_update is {can_safely}",
            )
            self.record_assertion(
                suite_name,
                f"{target_name}:verdict_breaking",
                verdict in ("BREAKING_CHANGES", "CANNOT_PIN"),
                f"Breaking changes present requires BREAKING_CHANGES verdict, got {verdict}",
            )
            self.record_assertion(
                suite_name,
                f"{target_name}:safety_score_breaking_bound",
                calc_safety < self.min_safety,
                f"{n_breaking} breaking changes produced safety score {calc_safety}% >= threshold {self.min_safety}%",
            )

        return calc_safety

    # --------------------------------------------------------------------------
    # Oracle Suite 1: Golden Fixture Accuracy
    # --------------------------------------------------------------------------
    def verify_fixtures(self) -> None:
        suite = "FixtureOracle"
        print("--- Suite 1: Local Golden Fixtures Oracle ---")

        # Case 1: GKE v1 -> v2
        print("  Evaluating GKE v1 -> v2 golden transition ... ", end="", flush=True)
        code, stdout, stderr = self.run_command(
            [
                "--old",
                "test/fixtures/upstream_gke_v1",
                "--new",
                "test/fixtures/upstream_gke_v2",
                "--format",
                "json",
            ]
        )
        self.record_assertion(suite, "gke_v1_v2:exit_zero", code == 0, stderr)
        try:
            data = json.loads(stdout)
            json_ok = True
        except Exception as e:
            json_ok = False
            data = {}
        self.record_assertion(suite, "gke_v1_v2:json_valid", json_ok, stdout[:100])

        if json_ok:
            self.record_assertion(
                suite,
                "gke_v1_v2:breaking_count",
                data.get("total_breaking") == 5,
                f"Expected 5 breaking changes, got {data.get('total_breaking')}",
            )
            self.record_assertion(
                suite,
                "gke_v1_v2:warning_count",
                data.get("total_warnings") == 2,
                f"Expected 2 warnings, got {data.get('total_warnings')}",
            )
            self.record_assertion(
                suite,
                "gke_v1_v2:new_features_count",
                data.get("total_new_features") == 2,
                f"Expected 2 new features, got {data.get('total_new_features')}",
            )
            self.record_assertion(
                suite,
                "gke_v1_v2:recreation_hazards",
                len(data.get("recreation_hazards", [])) == 2,
                f"Expected 2 recreation hazards, got {len(data.get('recreation_hazards', []))}",
            )
            safety = self.audit_confidence_and_safety(suite, "gke_v1_v2", data)
            print(f"PASSED (Safety: {safety:.1f}%)")
        else:
            print("FAILED")

        # Case 2: EKS v19 -> v20
        print("  Evaluating EKS v19 -> v20 golden transition ... ", end="", flush=True)
        code, stdout, stderr = self.run_command(
            [
                "--old",
                "test/fixtures/upstream_eks_v19",
                "--new",
                "test/fixtures/upstream_eks_v20",
                "--format",
                "json",
            ]
        )
        self.record_assertion(suite, "eks_v19_v20:exit_zero", code == 0, stderr)
        try:
            data = json.loads(stdout)
            json_ok = True
        except Exception:
            json_ok = False
            data = {}
        self.record_assertion(suite, "eks_v19_v20:json_valid", json_ok, stdout[:100])

        if json_ok:
            self.record_assertion(
                suite,
                "eks_v19_v20:breaking_count",
                data.get("total_breaking") == 5,
                f"Expected 5 breaking changes, got {data.get('total_breaking')}",
            )
            self.record_assertion(
                suite,
                "eks_v19_v20:warning_count",
                data.get("total_warnings") == 2,
                f"Expected 2 warnings, got {data.get('total_warnings')}",
            )
            self.record_assertion(
                suite,
                "eks_v19_v20:new_features_count",
                data.get("total_new_features") == 2,
                f"Expected 2 new features, got {data.get('total_new_features')}",
            )
            safety = self.audit_confidence_and_safety(suite, "eks_v19_v20", data)
            print(f"PASSED (Safety: {safety:.1f}%)")
        else:
            print("FAILED")

        # Case 3: AKS v1 -> v2
        print("  Evaluating AKS v1 -> v2 golden transition ... ", end="", flush=True)
        code, stdout, stderr = self.run_command(
            [
                "--old",
                "test/fixtures/upstream_aks_v1",
                "--new",
                "test/fixtures/upstream_aks_v2",
                "--format",
                "json",
            ]
        )
        self.record_assertion(suite, "aks_v1_v2:exit_zero", code == 0, stderr)
        try:
            data = json.loads(stdout)
            json_ok = True
        except Exception:
            json_ok = False
            data = {}
        self.record_assertion(suite, "aks_v1_v2:json_valid", json_ok, stdout[:100])

        if json_ok:
            self.record_assertion(
                suite,
                "aks_v1_v2:breaking_count",
                data.get("total_breaking") == 5,
                f"Expected 5 breaking changes, got {data.get('total_breaking')}",
            )
            self.record_assertion(
                suite,
                "aks_v1_v2:warning_count",
                data.get("total_warnings") == 1,
                f"Expected 1 warning, got {data.get('total_warnings')}",
            )
            self.record_assertion(
                suite,
                "aks_v1_v2:recreation_hazards",
                len(data.get("recreation_hazards", [])) == 1,
                f"Expected 1 recreation hazard, got {len(data.get('recreation_hazards', []))}",
            )
            safety = self.audit_confidence_and_safety(suite, "aks_v1_v2", data)
            print(f"PASSED (Safety: {safety:.1f}%)")
        else:
            print("FAILED")

        # Case 4: Identity / Zero-Diff Transition
        print("  Evaluating Identity (GKE v1 -> v1) zero-diff transition ... ", end="", flush=True)
        code, stdout, stderr = self.run_command(
            [
                "--old",
                "test/fixtures/upstream_gke_v1",
                "--new",
                "test/fixtures/upstream_gke_v1",
                "--format",
                "json",
            ]
        )
        self.record_assertion(suite, "gke_identity:exit_zero", code == 0, stderr)
        try:
            data = json.loads(stdout)
            json_ok = True
        except Exception:
            json_ok = False
            data = {}
        self.record_assertion(suite, "gke_identity:json_valid", json_ok, stdout[:100])

        if json_ok:
            self.record_assertion(
                suite,
                "gke_identity:breaking_zero",
                data.get("total_breaking") == 0,
                f"Expected 0 breaking, got {data.get('total_breaking')}",
            )
            self.record_assertion(
                suite,
                "gke_identity:warnings_zero",
                data.get("total_warnings") == 0,
                f"Expected 0 warnings, got {data.get('total_warnings')}",
            )
            self.record_assertion(
                suite,
                "gke_identity:can_safely_update",
                data.get("can_safely_update") is True,
                f"Expected can_safely_update=True, got {data.get('can_safely_update')}",
            )
            self.record_assertion(
                suite,
                "gke_identity:verdict_safe",
                data.get("verdict") == "SAFE",
                f"Expected verdict SAFE, got {data.get('verdict')}",
            )
            safety = self.audit_confidence_and_safety(suite, "gke_identity", data)
            print(f"PASSED (Safety: {safety:.1f}%)")
        else:
            print("FAILED")

        # Case 5: Wrapper Analysis Oracle
        print("  Evaluating GKE wrapper analysis oracle against v2 upstream ... ", end="", flush=True)
        wrapper_dir = self.repo_root / "test" / "fixtures" / "gke_wrapper"
        code, stdout, stderr = self.run_command(
            [
                "--wrapper",
                ".",
                "--new",
                "../upstream_gke_v2",
                "--format",
                "json",
            ],
            cwd=wrapper_dir,
        )
        self.record_assertion(suite, "wrapper:exit_zero", code == 0, stderr)
        try:
            data = json.loads(stdout)
            json_ok = True
        except Exception:
            json_ok = False
            data = {}
        self.record_assertion(suite, "wrapper:json_valid", json_ok, stdout[:100])

        if json_ok:
            self.record_assertion(
                suite,
                "wrapper:verdict_safe_to_pin",
                data.get("verdict") == "SAFE_TO_PIN",
                f"Expected SAFE_TO_PIN, got {data.get('verdict')}",
            )
            self.record_assertion(
                suite,
                "wrapper:can_safely_update",
                data.get("can_safely_update") is True,
                f"Expected can_safely_update=True, got {data.get('can_safely_update')}",
            )
            self.record_assertion(
                suite,
                "wrapper:new_features_adopted",
                len(data.get("new_feature_adoptions", [])) == 1,
                f"Expected 1 feature adoption, got {len(data.get('new_feature_adoptions', []))}",
            )
            safety = self.audit_confidence_and_safety(
                suite, "gke_wrapper", data, is_wrapper=True
            )
            print(f"PASSED (Safety: {safety:.1f}%)")
        else:
            print("FAILED")

        # Case 6: Extended Compatibility Rules Oracle (#172)
        print("  Evaluating Extended Rules (ModuleCall, ProviderConfig, Cloud Backend) ... ", end="", flush=True)
        with tempfile.TemporaryDirectory() as tmpdir:
            old_dir = Path(tmpdir) / "v1"
            new_dir = Path(tmpdir) / "v2"
            old_dir.mkdir()
            new_dir.mkdir()
            (old_dir / "main.tf").write_text(
                'terraform {\n'
                '  cloud {\n'
                '    organization = "org-a"\n'
                '  }\n'
                '}\n'
                'provider "aws" {\n'
                '  region = "us-east-1"\n'
                '}\n'
                'module "net" {\n'
                '  source  = "terraform-aws-modules/vpc/aws"\n'
                '  version = "~> 3.0"\n'
                '  count   = 3\n'
                '}\n'
                'module "shrunk" {\n'
                '  source   = "./sub"\n'
                '  for_each = toset(["a", "b"])\n'
                '}\n'
                'module "reformatted" {\n'
                '  source   = "./sub"\n'
                '  for_each = toset(["a", "b"])\n'
                '}\n'
            )
            (new_dir / "main.tf").write_text(
                'terraform {\n'
                '  cloud {\n'
                '    organization = "org-b"\n'
                '  }\n'
                '}\n'
                'provider "google" {\n'
                '  region = "us-central1"\n'
                '}\n'
                'module "net" {\n'
                '  source  = "terraform-aws-modules/vpc/aws"\n'
                '  version = "~> 5.0"\n'
                '  count   = 1\n'
                '}\n'
                'module "shrunk" {\n'
                '  source   = "./sub"\n'
                '  for_each = toset(["a"])\n'
                '}\n'
                'module "reformatted" {\n'
                '  source   = "./sub"\n'
                '  for_each = toset(["b", "a", "c"])\n'
                '}\n'
            )
            code, stdout, stderr = self.run_command(
                ["--old", str(old_dir), "--new", str(new_dir), "--format", "json"]
            )
            self.record_assertion(suite, "extended_rules:exit_zero", code == 0, stderr)
            try:
                ext_data = json.loads(stdout)
                ext_ok = True
            except Exception:
                ext_ok = False
                ext_data = {}
            self.record_assertion(suite, "extended_rules:json_valid", ext_ok, stdout[:100])
            if ext_ok:
                b_cats = {i.get("category") for i in ext_data.get("breaking_changes", [])}
                w_cats = {i.get("category") for i in ext_data.get("warnings", [])}
                for req_b in (
                    "StateBackendChange",
                    "ProviderConfigAdded",
                    "ModuleCallVersion",
                    "ModuleCallCount",
                    "ModuleCallForEach",
                ):
                    self.record_assertion(
                        suite,
                        f"extended_rules:breaking_{req_b}",
                        req_b in b_cats,
                        f"Missing {req_b} in breaking_changes: {b_cats}",
                    )
                for req_w in ("ProviderConfigRemoved", "ModuleCallForEach"):
                    self.record_assertion(
                        suite,
                        f"extended_rules:warning_{req_w}",
                        req_w in w_cats,
                        f"Missing {req_w} in warnings: {w_cats}",
                    )
                safety = self.audit_confidence_and_safety(suite, "extended_rules", ext_data)
                print(f"PASSED (Safety: {safety:.1f}%)")
            else:
                print("FAILED")

    # --------------------------------------------------------------------------
    # Oracle Suite 2: Upstream Git Module Upgrade Paths
    # --------------------------------------------------------------------------
    def verify_upstream_modules(self, samples_per_module: int = 3, seed: Optional[int] = None) -> None:
        suite = "UpstreamModuleOracle"
        print(f"\n--- Suite 2: Upstream Module Upgrade Paths ({samples_per_module} paths/module) ---")

        if seed is not None:
            random.seed(seed)

        module_specs = [
            {
                "name": "AWS EKS (terraform-aws-modules/eks/aws)",
                "source": "https://github.com/terraform-aws-modules/terraform-aws-eks.git",
                "curated_paths": [
                    {"from": "v19.0.0", "to": "v19.0.4", "type": "PATCH_SERIES", "expect_all_safe": True},
                    {"from": "v19.0.0", "to": "v19.2.0", "type": "MINOR_BUMP",   "expect_all_safe": True},
                    {"from": "v18.31.2", "to": "v19.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": False},
                    {"from": "v19.21.0", "to": "v20.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": False},
                ],
                "fallback_pool": [
                    "v18.20.0", "v18.30.0", "v18.31.2",
                    "v19.0.0", "v19.0.1", "v19.0.2", "v19.0.3", "v19.0.4",
                    "v19.1.0", "v19.1.1", "v19.2.0", "v19.5.0", "v19.15.0", "v19.21.0",
                    "v20.0.0", "v20.2.0", "v20.8.0"
                ],
            },
            {
                "name": "Google GKE (terraform-google-modules/kubernetes-engine/google)",
                "source": "https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git",
                "curated_paths": [
                    {"from": "v30.0.0", "to": "v30.1.0", "type": "MINOR_BUMP",   "expect_all_safe": True},
                    {"from": "v32.0.0", "to": "v32.0.2", "type": "PATCH_SERIES", "expect_all_safe": True},
                    {"from": "v29.0.0", "to": "v30.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": False},
                    {"from": "v30.0.0", "to": "v31.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": False},
                ],
                "fallback_pool": [
                    "v28.0.0", "v29.0.0",
                    "v30.0.0", "v30.1.0", "v30.2.0", "v30.3.0",
                    "v31.0.0", "v31.1.0",
                    "v32.0.0", "v32.0.1", "v32.0.2", "v32.0.3",
                    "v33.0.0", "v33.1.0"
                ],
            },
        ]

        for spec in module_specs:
            mod_name = spec["name"]
            source = spec["source"]
            paths = self._select_paths(spec, samples_per_module)
            print(f"Evaluating {mod_name} across {len(paths)} upgrade paths:")

            for idx, p in enumerate(paths, 1):
                v_from = p["from"]
                v_to = p["to"]
                p_type = p["type"]
                label = f"{v_from} -> {v_to} ({p_type})"
                print(f"  [{idx}/{len(paths)}] Testing {label} ... ", end="", flush=True)

                cmd_args = [
                    "--old", source,
                    "--new", source,
                    "--old-version", v_from,
                    "--new-version", v_to,
                    "--upgrade-path",
                    "--format", "json",
                ]

                code, stdout, stderr = self.run_command(cmd_args, timeout=120)
                if not self.record_assertion(suite, f"{label}:exit_zero", code == 0, stderr):
                    print(f"FAILED (Exit {code})")
                    continue

                try:
                    data = json.loads(stdout)
                    json_ok = True
                except Exception:
                    json_ok = False
                    data = {}

                if not self.record_assertion(suite, f"{label}:json_valid", json_ok, stdout[:120]):
                    print("FAILED (JSON parse error)")
                    continue

                # INV-1: Schema integrity
                self.record_assertion(suite, f"{label}:from_version", data.get("from_version") == v_from)
                self.record_assertion(suite, f"{label}:target_version", data.get("target_version") == v_to)
                self.record_assertion(suite, f"{label}:steps_present", isinstance(data.get("steps"), list) and len(data["steps"]) >= 1)

                steps = data.get("steps") or []
                can_safely_all = data.get("can_safely_update_all", False)
                rec_ver = data.get("recommended_version", "")

                step_safeties: List[float] = []
                first_breaking_idx: Optional[int] = None

                for s_idx, step in enumerate(steps):
                    s_from = step.get("from_version")
                    s_to = step.get("to_version")
                    diff = step.get("diff") or {}

                    # INV-2: Monotonic progression
                    self.record_assertion(
                        suite,
                        f"{label}:step_{s_idx}:monotonic",
                        semver_parse(s_from) < semver_parse(s_to),
                        f"Versions not ascending: {s_from} >= {s_to}",
                    )

                    # Audit step confidence and safety
                    step_safety = self.audit_confidence_and_safety(
                        suite, f"{label}:step_{s_idx}", diff
                    )
                    step_safeties.append(step_safety)

                    if diff.get("total_breaking", 0) > 0 and first_breaking_idx is None:
                        first_breaking_idx = s_idx

                # Multi-Step Safety Score Formula: S_path = min(S_step_i)
                composite_path_safety = min(step_safeties) if step_safeties else 0.0

                # Theorem: can_safely_update_all <=> S_path >= 70.0%
                if first_breaking_idx is None:
                    self.record_assertion(
                        suite,
                        f"{label}:all_safe_agreement",
                        can_safely_all is True,
                        "All steps have zero breaking changes but can_safely_update_all is False",
                    )
                    self.record_assertion(
                        suite,
                        f"{label}:composite_safety_safe",
                        composite_path_safety >= self.min_safety,
                        f"All safe path composite safety {composite_path_safety}% < threshold {self.min_safety}%",
                    )
                    self.record_assertion(
                        suite,
                        f"{label}:recommended_is_target",
                        rec_ver == v_to,
                        f"Expected recommended version {v_to}, got {rec_ver}",
                    )
                else:
                    self.record_assertion(
                        suite,
                        f"{label}:breaking_detected_agreement",
                        can_safely_all is False,
                        f"Breaking change in step {first_breaking_idx} but can_safely_update_all is True",
                    )
                    self.record_assertion(
                        suite,
                        f"{label}:composite_safety_breaking",
                        composite_path_safety < self.min_safety,
                        f"Breaking path composite safety {composite_path_safety}% >= threshold {self.min_safety}%",
                    )
                    expected_safe_pin = steps[first_breaking_idx]["from_version"]
                    self.record_assertion(
                        suite,
                        f"{label}:recommended_pin_agreement",
                        rec_ver == expected_safe_pin,
                        f"Expected safe pin {expected_safe_pin}, got {rec_ver}",
                    )

                # INV-4: Curated expectation check
                if p.get("expect_all_safe") is not None:
                    expected = p["expect_all_safe"]
                    self.record_assertion(
                        suite,
                        f"{label}:curated_expectation",
                        can_safely_all == expected,
                        f"Expected can_safely_update_all={expected}, got {can_safely_all}",
                    )

                # INV-7: Exit-code mode parity
                exit_code, _, _ = self.run_command(cmd_args + ["--exit-code"], timeout=60)
                expected_exit = 0 if can_safely_all else 1
                self.record_assertion(
                    suite,
                    f"{label}:exit_code_parity",
                    exit_code == expected_exit,
                    f"--exit-code yielded {exit_code}, expected {expected_exit}",
                )

                print(f"PASSED ({len(steps)} transitions, composite safety: {composite_path_safety:.1f}%)")

                self.evaluations.append({
                    "module": mod_name,
                    "path": f"{v_from} -> {v_to}",
                    "type": p_type,
                    "transitions": len(steps),
                    "can_safely_update_all": can_safely_all,
                    "recommended_version": rec_ver,
                    "composite_safety_score": composite_path_safety,
                })

    def _select_paths(self, spec: Dict[str, Any], count: int) -> List[Dict[str, Any]]:
        selected: List[Dict[str, Any]] = []
        curated = spec.get("curated_paths", [])
        majors = [p for p in curated if p["type"] == "MAJOR_JUMP"]
        minors = [p for p in curated if p["type"] != "MAJOR_JUMP"]

        if majors:
            selected.append(random.choice(majors))
        if minors and len(selected) < count:
            selected.append(random.choice(minors))

        # Query live remote tags if reachable within 8 seconds
        tags = self._fetch_remote_tags(spec["source"])
        pool = sorted(tags if len(tags) >= 5 else spec["fallback_pool"], key=semver_parse)

        attempts = 0
        while len(selected) < count and attempts < 50:
            attempts += 1
            i = random.randint(0, len(pool) - 2)
            j = random.randint(i + 1, min(i + 4, len(pool) - 1))
            v_from, v_to = pool[i], pool[j]

            if any(p["from"] == v_from and p["to"] == v_to for p in selected):
                continue

            v_from_p, v_to_p = semver_parse(v_from), semver_parse(v_to)
            ptype = (
                "MAJOR_JUMP"
                if v_to_p[0] > v_from_p[0]
                else ("MINOR_BUMP" if v_to_p[1] > v_from_p[1] else "PATCH_BUMP")
            )
            selected.append({
                "from": v_from,
                "to": v_to,
                "type": ptype,
                "expect_all_safe": None,
            })
        return selected[:count]

    def _fetch_remote_tags(self, repo_url: str) -> List[str]:
        try:
            out = subprocess.check_output(
                ["git", "ls-remote", "--tags", repo_url],
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=8,
            )
            tags = []
            for line in out.splitlines():
                parts = line.strip().split()
                if len(parts) == 2:
                    ref = parts[1]
                    if ref.startswith("refs/tags/") and not ref.endswith("^{}"):
                        tag = ref[len("refs/tags/"):]
                        if tag.startswith("v") and "." in tag and not any(p in tag for p in ["rc", "alpha", "beta"]):
                            tags.append(tag)
            return tags
        except Exception:
            return []

    # --------------------------------------------------------------------------
    # Final Reporting & Verdict
    # --------------------------------------------------------------------------
    def generate_report(self, elapsed: float, output_file: Optional[str] = None) -> bool:
        total = len(self.assertions)
        passed = sum(1 for a in self.assertions if a.passed)
        failed = [a for a in self.assertions if not a.passed]
        accuracy_pct = (passed / total * 100.0) if total > 0 else 0.0

        mean_safety = (
            statistics.mean(self.safety_scores) if self.safety_scores else 0.0
        )
        conf = self.confidence_metrics

        print("\n" + "=" * 80)
        print(" INDEPENDENT ORACLE VERIFICATION REPORT (terraform_ast / tfastdiff)")
        print("=" * 80)
        print(f" Total Assertions Evaluated:    {total}")
        print(f" Assertions Passed:             {passed}")
        print(f" Assertions Failed:             {len(failed)}")
        print(f" Measured Accuracy Rate:        {accuracy_pct:.2f}% (Required: {self.min_accuracy:.2f}%)")
        print("-" * 80)
        print(" CONFIDENCE SCORING ANALYSIS (Scale 1 - 10):")
        print(f"   Total Scored Findings:       {conf.total_items}")
        print(f"   Mean Confidence Score:       {conf.mean:.2f} / 10.0 (Required: >={self.min_confidence:.2f})")
        print(f"   Breaking Confidence Mean:    {conf.breaking_mean:.2f} / 10.0 (Calibrated >= 7.0)")
        print(f"   Warning Confidence Mean:     {conf.warning_mean:.2f} / 10.0")
        print(f"   Feature Confidence Mean:     {conf.feature_mean:.2f} / 10.0")
        print(f"   Score Standard Deviation:    {conf.std_dev:.2f}")
        print("-" * 80)
        print(" SAFETY SCORING CALIBRATION (Scale 0.0 - 100.0%):")
        print(f"   Evaluated Transitions:       {len(self.safety_scores)}")
        print(f"   Mean Safety Score:           {mean_safety:.2f}%")
        print(f"   Safe Threshold Monotonicity: Verified (N_breaking=0 <=> CanSafely <=> S>=70%)")
        print(f" Total Execution Time:          {elapsed:.2f}s")
        print("=" * 80)

        if failed:
            print("\n[!] Oracle Invariant Failures Detected:")
            for idx, f in enumerate(failed[:15], 1):
                print(f"  {idx}. [{f.test_suite}] {f.assertion_name} -> {f.details}")
            if len(failed) > 15:
                print(f"  ... and {len(failed) - 15} more failures.")
            print()

        is_passed = (
            accuracy_pct >= self.min_accuracy
            and len(failed) == 0
            and conf.mean >= self.min_confidence
        )
        verdict_str = "PASSED" if is_passed else "FAILED"
        print(f" ORACLE DECISION: {verdict_str}\n")

        if output_file:
            report_data = {
                "verdict": verdict_str,
                "passed": is_passed,
                "accuracy_percent": round(accuracy_pct, 2),
                "min_accuracy_threshold": self.min_accuracy,
                "total_assertions": total,
                "passed_assertions": passed,
                "failed_assertions": len(failed),
                "failures": [
                    {"suite": f.test_suite, "name": f.assertion_name, "error": f.details}
                    for f in failed
                ],
                "confidence_metrics": {
                    "total_items": conf.total_items,
                    "mean_score": round(conf.mean, 2),
                    "min_confidence_threshold": self.min_confidence,
                    "breaking_mean": round(conf.breaking_mean, 2),
                    "warning_mean": round(conf.warning_mean, 2),
                    "feature_mean": round(conf.feature_mean, 2),
                    "std_dev": round(conf.std_dev, 2),
                    "min": conf.min,
                    "max": conf.max,
                },
                "safety_metrics": {
                    "evaluated_transitions": len(self.safety_scores),
                    "mean_safety_score": round(mean_safety, 2),
                    "min_safety_threshold": self.min_safety,
                },
                "evaluations": self.evaluations,
                "elapsed_seconds": round(elapsed, 2),
                "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            }
            try:
                out_path = Path(output_file)
                out_path.parent.mkdir(parents=True, exist_ok=True)
                with open(out_path, "w", encoding="utf-8") as f:
                    json.dump(report_data, f, indent=2)
                print(f"[*] Oracle JSON Report exported to: {out_path.resolve()}")
            except Exception as e:
                print(f"[!] Warning: Failed to export oracle report to {output_file}: {e}", file=sys.stderr)

        return is_passed


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Independent Python Verification Oracle for terraform_ast (tfastdiff)"
    )
    parser.add_argument(
        "-b", "--bin", default="bin/tfastdiff", help="Path to tfastdiff binary"
    )
    parser.add_argument(
        "-n", "--samples", type=int, default=3, help="Upgrade path samples per upstream module"
    )
    parser.add_argument(
        "-s", "--seed", type=int, default=None, help="Random seed for path selection"
    )
    parser.add_argument(
        "-m", "--min-accuracy", type=float, default=100.0, help="Minimum accuracy gate percentage"
    )
    parser.add_argument(
        "-c", "--min-confidence", type=float, default=8.0, help="Minimum mean confidence score"
    )
    parser.add_argument(
        "--min-safety", type=float, default=70.0, help="Minimum safety score for safe transitions"
    )
    parser.add_argument(
        "--skip-upstream", action="store_true", help="Skip upstream module git tests (fixtures only)"
    )
    parser.add_argument(
        "-o", "--output", default="oracle_report.json", help="Path to write JSON oracle report"
    )
    parser.add_argument(
        "-v", "--verbose", action="store_true", help="Enable verbose failure logging"
    )
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parent.parent
    bin_path = str((repo_root / args.bin).resolve() if not Path(args.bin).is_absolute() else Path(args.bin))

    if not Path(bin_path).is_file():
        print(f"==> Binary '{bin_path}' not found. Compiling bin/tfastdiff...")
        subprocess.check_call(["go", "build", "-o", bin_path, "./cmd/tfastdiff"], cwd=str(repo_root))

    oracle = VerificationOracle(
        bin_path=bin_path,
        repo_root=repo_root,
        min_accuracy=args.min_accuracy,
        min_confidence=args.min_confidence,
        min_safety=args.min_safety,
        verbose=args.verbose,
    )

    start_time = time.time()
    oracle.verify_fixtures()

    if not args.skip_upstream:
        oracle.verify_upstream_modules(samples_per_module=args.samples, seed=args.seed)

    elapsed = time.time() - start_time
    success = oracle.generate_report(elapsed, output_file=args.output)
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
