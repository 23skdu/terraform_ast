#!/usr/bin/env bash
#
# Accuracy Gate & Confidence Scoring Validation for terraform_ast (tfastdiff).
#
# Validates tfastdiff against upstream AWS EKS and Google GKE module releases,
# testing random and structured upgrade version paths across patch, minor, and major jumps.
# Evaluates semantic invariants, safe pin recommendations, exit codes, and confidence scores.
#
# Usage:
#   scripts/validate_upgrade_accuracy.sh [options]
#
# Options:
#   -n, --samples <N>        Number of upgrade paths to test per module (default: 3)
#   -s, --seed <INT>         Random seed for reproducible path selection (default: random)
#   -m, --min-accuracy <PCT> Minimum accuracy gate threshold in percent (default: 100.0)
#   -b, --bin <PATH>         Path to tfastdiff binary (default: bin/tfastdiff)
#   -o, --output <FILE>      Save validation metrics and report to JSON file
#   -v, --verbose            Print verbose evaluation logs
#   -h, --help               Show this help message

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

SAMPLES=3
SEED=""
MIN_ACCURACY="100.0"
BIN_PATH="${REPO_ROOT}/bin/tfastdiff"
OUTPUT_FILE=""
VERBOSE=0

show_help() {
  grep -E '^# ' "$0" | cut -c 3-
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--samples)
      SAMPLES="$2"
      shift 2
      ;;
    -s|--seed)
      SEED="$2"
      shift 2
      ;;
    -m|--min-accuracy)
      MIN_ACCURACY="$2"
      shift 2
      ;;
    -b|--bin)
      BIN_PATH="$2"
      shift 2
      ;;
    -o|--output)
      OUTPUT_FILE="$2"
      shift 2
      ;;
    -v|--verbose)
      VERBOSE=1
      shift
      ;;
    -h|--help)
      show_help
      ;;
    *)
      echo "Unknown option: $1" >&2
      show_help
      ;;
  esac
done

if [[ ! -x "$BIN_PATH" ]]; then
  echo "==> Binary $BIN_PATH not found or not executable. Building..."
  (cd "$REPO_ROOT" && go build -o "$BIN_PATH" ./cmd/tfastdiff)
fi

echo "================================================================================"
echo " tfastdiff Upgrade Accuracy Gate & Confidence Scoring Validation"
echo "================================================================================"
echo " Binary:           $BIN_PATH"
echo " Samples / Module: $SAMPLES"
echo " Min Accuracy:     ${MIN_ACCURACY}%"
[[ -n "$SEED" ]] && echo " Random Seed:      $SEED"
[[ -n "$OUTPUT_FILE" ]] && echo " Output Artifact:  $OUTPUT_FILE"
echo "================================================================================"

exec python3 - "$BIN_PATH" "$SAMPLES" "$SEED" "$MIN_ACCURACY" "$OUTPUT_FILE" "$VERBOSE" << 'EOF'
import sys
import os
import json
import random
import subprocess
import time

bin_path = sys.argv[1]
samples_per_module = int(sys.argv[2])
seed_arg = sys.argv[3]
min_accuracy = float(sys.argv[4])
output_file = sys.argv[5] if len(sys.argv) > 5 and sys.argv[5] else None
verbose = int(sys.argv[6]) == 1

if seed_arg:
    random.seed(int(seed_arg))

MODULE_SPECS = [
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
        ]
    },
    {
        "name": "Google GKE (terraform-google-modules/kubernetes-engine/google)",
        "source": "https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git",
        "curated_paths": [
            {"from": "v30.0.0", "to": "v30.1.0", "type": "MINOR_BUMP",   "expect_all_safe": True},
            {"from": "v32.0.0", "to": "v32.0.2", "type": "PATCH_SERIES", "expect_all_safe": True},
            {"from": "v29.0.0", "to": "v30.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": False},
            {"from": "v30.0.0", "to": "v31.0.0", "type": "MAJOR_JUMP",  "expect_all_safe": True},
        ],
        "fallback_pool": [
            "v28.0.0", "v29.0.0",
            "v30.0.0", "v30.1.0", "v30.2.0", "v30.3.0",
            "v31.0.0", "v31.1.0",
            "v32.0.0", "v32.0.1", "v32.0.2", "v32.0.3",
            "v33.0.0", "v33.1.0"
        ]
    }
]

def semver_parse(v):
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


def fetch_remote_tags(repo_url):
    try:
        out = subprocess.check_output(
            ["git", "ls-remote", "--tags", repo_url],
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=10
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

def select_paths(spec, count):
    selected = []
    curated = spec["curated_paths"]
    majors = [p for p in curated if p["type"] == "MAJOR_JUMP"]
    minors = [p for p in curated if p["type"] != "MAJOR_JUMP"]

    if majors:
        selected.append(random.choice(majors))
    if minors and len(selected) < count:
        selected.append(random.choice(minors))

    # Fetch live remote tags if available, otherwise use fallback pool
    live_tags = fetch_remote_tags(spec["source"])
    pool = sorted(live_tags if len(live_tags) >= 5 else spec["fallback_pool"], key=semver_parse)

    attempts = 0
    while len(selected) < count and attempts < 60:
        attempts += 1
        i = random.randint(0, len(pool) - 2)
        j = random.randint(i + 1, min(i + 4, len(pool) - 1))
        v_from, v_to = pool[i], pool[j]

        if any(p["from"] == v_from and p["to"] == v_to for p in selected):
            continue

        v_from_p, v_to_p = semver_parse(v_from), semver_parse(v_to)
        ptype = "MAJOR_JUMP" if v_to_p[0] > v_from_p[0] else ("MINOR_BUMP" if v_to_p[1] > v_from_p[1] else "PATCH_BUMP")
        selected.append({
            "from": v_from,
            "to": v_to,
            "type": ptype,
            "expect_all_safe": None
        })
    return selected[:count]

total_assertions = 0
passed_assertions = 0
failures = []
all_confidence_scores = []
results_summary = []

def record_assertion(test_name, condition, error_msg):
    global total_assertions, passed_assertions
    total_assertions += 1
    if condition:
        passed_assertions += 1
        return True
    else:
        failures.append(f"[{test_name}] {error_msg}")
        return False

print(f"\n[*] Commencing Accuracy Gate Evaluation across {len(MODULE_SPECS)} Module Suites...\n")

start_time = time.time()

for spec in MODULE_SPECS:
    mod_name = spec["name"]
    source = spec["source"]
    paths = select_paths(spec, samples_per_module)
    print(f"--- Module: {mod_name} ({len(paths)} paths) ---")

    for idx, path in enumerate(paths, 1):
        v_from = path["from"]
        v_to = path["to"]
        p_type = path["type"]
        path_label = f"{v_from} -> {v_to} ({p_type})"
        print(f"  [{idx}/{len(paths)}] Testing upgrade path: {path_label} ... ", end="", flush=True)

        cmd = [
            bin_path,
            "--old", source,
            "--new", source,
            "--old-version", v_from,
            "--new-version", v_to,
            "--upgrade-path",
            "--format", "json"
        ]

        try:
            res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=120)
        except subprocess.TimeoutExpired:
            print("FAILED (Timeout >120s)")
            record_assertion(path_label, False, "tfastdiff command timed out after 120s")
            continue

        if not record_assertion(path_label, res.returncode == 0, f"Process exited with code {res.returncode}: {res.stderr.strip()}"):
            print(f"FAILED (Exit {res.returncode})")
            continue

        raw_json = res.stdout.strip()
        try:
            data = json.loads(raw_json)
            json_valid = True
        except Exception as e:
            json_valid = False
            data = {}

        if not record_assertion(path_label, json_valid, f"Invalid JSON output: {raw_json[:120]}"):
            print("FAILED (Corrupt JSON)")
            continue

        # Invariant 1: Top-level schema properties
        record_assertion(path_label, data.get("from_version") == v_from, f"from_version mismatch: got {data.get('from_version')}, expected {v_from}")
        record_assertion(path_label, data.get("target_version") == v_to, f"target_version mismatch: got {data.get('target_version')}, expected {v_to}")
        record_assertion(path_label, "can_safely_update_all" in data, "Missing can_safely_update_all boolean")
        record_assertion(path_label, "steps" in data and isinstance(data["steps"], list) and len(data["steps"]) >= 1, "steps array must have at least 1 entry")

        steps = data.get("steps", [])
        can_safely_all = data.get("can_safely_update_all", False)
        rec_ver = data.get("recommended_version", "")

        # Invariant 2: Step-by-step verification
        first_breaking_idx = None
        path_scores = []

        for s_idx, step in enumerate(steps):
            s_from = step.get("from_version")
            s_to = step.get("to_version")
            s_safe = step.get("can_safely_update")
            diff = step.get("diff") or {}

            # Monotonic ascending check
            s_from_p, s_to_p = semver_parse(s_from), semver_parse(s_to)
            record_assertion(path_label, s_from_p < s_to_p, f"Step {s_idx} versions not strictly ascending: {s_from} >= {s_to}")

            # Verdict & Safety logic
            total_breaking = diff.get("total_breaking", 0)
            verdict = diff.get("verdict", "")
            bump_type = diff.get("bump_type", "")

            valid_verdicts = {"SAFE", "BREAKING_CHANGES", "WARNINGS_ONLY", "DOWNGRADE"}
            record_assertion(path_label, verdict in valid_verdicts, f"Step {s_idx} unknown verdict {verdict}")

            if total_breaking > 0:
                record_assertion(path_label, s_safe is False, f"Step {s_idx} has {total_breaking} breaking changes but can_safely_update is True")
                record_assertion(path_label, verdict == "BREAKING_CHANGES", f"Step {s_idx} breaking change requires BREAKING_CHANGES verdict, got {verdict}")
                if first_breaking_idx is None:
                    first_breaking_idx = s_idx
            else:
                record_assertion(path_label, s_safe is True, f"Step {s_idx} has 0 breaking changes but can_safely_update is False")
                record_assertion(path_label, verdict in {"SAFE", "WARNINGS_ONLY"}, f"Step {s_idx} safe update verdict must be SAFE or WARNINGS_ONLY, got {verdict}")

            # Confidence score validation
            for bc in diff.get("breaking_changes") or []:
                score = bc.get("confidence_score")
                if score is not None:
                    record_assertion(path_label, 1 <= score <= 10, f"Step {s_idx} breaking change confidence score {score} out of [1, 10]")
                    record_assertion(path_label, score >= 7, f"Step {s_idx} breaking change confidence score {score} unexpectedly low (< 7)")
                    path_scores.append(score)
                    all_confidence_scores.append(score)

            for w in diff.get("warnings") or []:
                score = w.get("confidence_score")
                if score is not None:
                    record_assertion(path_label, 1 <= score <= 10, f"Step {s_idx} warning confidence score {score} out of [1, 10]")
                    path_scores.append(score)
                    all_confidence_scores.append(score)

            for nf in diff.get("new_features") or []:
                score = nf.get("confidence_score")
                if score is not None:
                    record_assertion(path_label, 1 <= score <= 10, f"Step {s_idx} new feature confidence score {score} out of [1, 10]")
                    path_scores.append(score)
                    all_confidence_scores.append(score)

        # Invariant 3: Recommended Version Agreement
        if first_breaking_idx is None:
            record_assertion(path_label, can_safely_all is True, "All steps safe implies can_safely_update_all must be True")
            record_assertion(path_label, rec_ver == v_to, f"All steps safe: recommended_version should be target {v_to}, got {rec_ver}")
        else:
            record_assertion(path_label, can_safely_all is False, "Breaking step present implies can_safely_update_all must be False")
            expected_safe_pin = steps[first_breaking_idx]["from_version"]
            record_assertion(path_label, rec_ver == expected_safe_pin, f"Breaking step at index {first_breaking_idx}: recommended_version should be {expected_safe_pin}, got {rec_ver}")

        # Invariant 4: Verified expectation if curated
        if path.get("expect_all_safe") is not None:
            expected = path["expect_all_safe"]
            record_assertion(path_label, can_safely_all == expected, f"Curated path expectation violated: expected can_safely_update_all={expected}, got {can_safely_all}")

        # Invariant 5: Exit-code mode parity verification
        exit_cmd = cmd + ["--exit-code"]
        exit_res = subprocess.run(exit_cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        expected_code = 0 if can_safely_all else 1
        record_assertion(path_label, exit_res.returncode == expected_code, f"--exit-code produced {exit_res.returncode}, expected {expected_code}")

        step_count = len(steps)
        mean_path_score = sum(path_scores) / len(path_scores) if path_scores else 10.0
        print(f"PASSED ({step_count} transitions, score: {mean_path_score:.1f}/10)")

        results_summary.append({
            "module": mod_name,
            "path": f"{v_from} -> {v_to}",
            "type": p_type,
            "transitions": step_count,
            "all_safe": can_safely_all,
            "recommended_version": rec_ver,
            "mean_confidence": round(mean_path_score, 2),
            "status": "PASS"
        })

elapsed = time.time() - start_time
accuracy_pct = (passed_assertions / total_assertions * 100.0) if total_assertions > 0 else 0.0
mean_conf = (sum(all_confidence_scores) / len(all_confidence_scores)) if all_confidence_scores else 9.5

print("\n" + "=" * 80)
print(" UPGRADE ACCURACY & CONFIDENCE SCORING REPORT")
print("=" * 80)
print(f" Total Assertions Evaluated:  {total_assertions}")
print(f" Assertions Passed:           {passed_assertions}")
print(f" Assertions Failed:           {len(failures)}")
print(f" Mean Confidence Score:       {mean_conf:.2f} / 10.0")
print(f" Measured Accuracy Rate:      {accuracy_pct:.2f}% (Threshold: {min_accuracy:.2f}%)")
print(f" Total Execution Time:        {elapsed:.2f}s")
print("-" * 80)

if failures:
    print("\n[!] Invariant Violations Detected:")
    for f in failures:
        print(f"  - {f}")
    print()

status = "PASSED" if accuracy_pct >= min_accuracy and len(failures) == 0 else "FAILED"
print(f" GATE VERDICT: {status}\n")

if output_file:
    report_data = {
        "verdict": status,
        "accuracy_percent": round(accuracy_pct, 2),
        "min_accuracy_threshold": min_accuracy,
        "mean_confidence_score": round(mean_conf, 2),
        "total_assertions": total_assertions,
        "passed_assertions": passed_assertions,
        "failed_assertions": len(failures),
        "failures": failures,
        "elapsed_seconds": round(elapsed, 2),
        "paths": results_summary
    }
    try:
        os.makedirs(os.path.dirname(os.path.abspath(output_file)), exist_ok=True)
        with open(output_file, "w") as f:
            json.dump(report_data, f, indent=2)
        print(f"[*] Artifact saved to {output_file}")
    except Exception as e:
        print(f"[!] Warning: failed to write output file {output_file}: {e}", file=sys.stderr)

if status != "PASSED":
    sys.exit(1)
EOF
