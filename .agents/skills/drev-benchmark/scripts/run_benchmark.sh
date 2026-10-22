#!/usr/bin/env bash
# ==============================================================================
# git-drev Deterministic Real-World Benchmark Runner (Eclipse SDK I-Builds)
# ==============================================================================
# Reproduces the 5-build Eclipse SDK benchmark defined in:
#   docs/user-guide/storage-and-retention.md (Section 6)
#
# Usage:
#   ./.agents/skills/drev-benchmark/scripts/run_benchmark.sh \
#     --host <ssh_host> \
#     --path <remote_path> \
#     [--bin ./bin/git-drev] \
#     [--builds I2026...,I2026...] \
#     [--cache-dir /path/to/cache] \
#     [--clean-before] \
#     [--clean-after] \
#     [--output report.md] \
#     [--self-test]
# ==============================================================================

set -euo pipefail

export LC_ALL=C
export LANG=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"

SSH_HOST=""
REMOTE_PATH=""
DREV_BIN="${REPO_ROOT}/bin/git-drev"
BUILDS_CSV=""
CACHE_DIR=""
CLEAN_BEFORE="false"
CLEAN_AFTER="false"
OUTPUT_FILE=""
SELF_TEST="false"

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Required (unless --self-test):
  --host <host>           Remote SSH host or alias (e.g., drs)
  --path <path>           Remote storage directory path (e.g., /volume1/storage_drs/data/git-drev-benchmark)

Options:
  --bin <path>            Path to git-drev binary (default: ./bin/git-drev)
  --builds <b1,b2,...>    Explicit comma-separated list of 5 Eclipse I-build IDs
  --cache-dir <dir>       Directory to cache downloaded .tar.gz archives and download times
  --clean-before          Remove existing <path> on <host> before running the benchmark
  --clean-after           Remove <path> on <host> after generating the report
  --output <file>         Write rendered Markdown report to <file> (in addition to stdout)
  --self-test             Run deterministic formula & Markdown rendering verification test
  -h, --help              Show this help message
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      SSH_HOST="${2:-}"
      shift 2
      ;;
    --path)
      REMOTE_PATH="${2:-}"
      shift 2
      ;;
    --bin)
      DREV_BIN="${2:-}"
      shift 2
      ;;
    --builds)
      BUILDS_CSV="${2:-}"
      shift 2
      ;;
    --cache-dir)
      CACHE_DIR="${2:-}"
      shift 2
      ;;
    --clean-before)
      CLEAN_BEFORE="true"
      shift
      ;;
    --clean-after)
      CLEAN_AFTER="true"
      shift
      ;;
    --output)
      OUTPUT_FILE="${2:-}"
      shift 2
      ;;
    --self-test)
      SELF_TEST="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "[benchmark] Error: Unknown option '$1'" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${SELF_TEST}" == "false" ]]; then
  if [[ -z "${SSH_HOST}" || -z "${REMOTE_PATH}" ]]; then
    echo "[benchmark] Error: --host and --path are required." >&2
    usage >&2
    exit 2
  fi
  if [[ ! -x "${DREV_BIN}" ]]; then
    echo "[benchmark] Building git-drev binary at '${DREV_BIN}'..." >&2
    (
      cd "${REPO_ROOT}"
      mkdir -p "$(dirname "${DREV_BIN}")"
      go build -ldflags "-s -w -X 'github.com/bvarnai/git-drev/internal/version.Version=v0.1.0-dev' -X 'github.com/bvarnai/git-drev/internal/version.Commit=$(git rev-parse --short=7 HEAD)' -X 'github.com/bvarnai/git-drev/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" -trimpath -o "${DREV_BIN}" ./cmd/git-drev
    )
  fi
  DREV_BIN="$(cd "$(dirname "${DREV_BIN}")" && pwd)/$(basename "${DREV_BIN}")"
fi

python3 - "${SSH_HOST}" "${REMOTE_PATH}" "${DREV_BIN}" "${BUILDS_CSV}" "${CACHE_DIR}" "${CLEAN_BEFORE}" "${CLEAN_AFTER}" "${OUTPUT_FILE}" "${SELF_TEST}" <<'PYEOF'
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

(
    ssh_host,
    remote_path,
    drev_bin,
    builds_csv,
    cache_dir,
    clean_before_str,
    clean_after_str,
    output_file,
    self_test_str,
) = sys.argv[1:10]

clean_before = clean_before_str.lower() == "true"
clean_after = clean_after_str.lower() == "true"
self_test = self_test_str.lower() == "true"


def log(msg: str) -> None:
    sys.stderr.write(f"[benchmark] {msg}\n")
    sys.stderr.flush()


def parse_size_to_mb(size_str: str) -> float:
    """Parse human-readable du size like '483M', '1.5G', '512K' into MB (MiB)."""
    s = size_str.strip().upper()
    m = re.match(r"^([0-9.]+)\s*([KMGTPE]?)(?:I?B)?$", s)
    if not m:
        return 0.0
    val = float(m.group(1))
    unit = m.group(2)
    mult = {
        "": 1.0 / (1024.0 * 1024.0),
        "K": 1.0 / 1024.0,
        "M": 1.0,
        "G": 1024.0,
        "T": 1024.0 * 1024.0,
    }.get(unit, 1.0)
    return val * mult


def parse_rsync_stats(output: str) -> tuple[int, int]:
    """Return (files_transferred, total_bytes_wire_or_file) from rsync --stats output."""
    files_transferred = 0
    m_files = re.search(
        r"Number of (?:regular )?files transferred:\s*([0-9,]+)", output
    )
    if m_files:
        files_transferred = int(m_files.group(1).replace(",", ""))
    return files_transferred, 0


def parse_rsync_wire_bytes(output: str, is_upload: bool) -> tuple[int, float]:
    """
    Extract (files_transferred, wire_mib) from rsync --stats output.
    For upload (put), uses 'Total bytes sent' (falls back to 'Total transferred file size').
    For download (get), uses 'Total bytes received' (falls back to 'Total transferred file size').
    """
    files_transferred = 0
    m_files = re.search(
        r"Number of (?:regular )?files transferred:\s*([0-9,]+)", output
    )
    if m_files:
        files_transferred = int(m_files.group(1).replace(",", ""))

    target_key = "Total bytes sent" if is_upload else "Total bytes received"
    m_bytes = re.search(rf"{target_key}:\s*([0-9,]+)", output)
    if not m_bytes:
        m_bytes = re.search(r"Total transferred file size:\s*([0-9,]+)", output)
    raw_bytes = int(m_bytes.group(1).replace(",", "")) if m_bytes else 0
    return files_transferred, raw_bytes / (1024.0 * 1024.0)


def render_report(data: dict) -> str:
    """Render the deterministic Markdown benchmark report matching storage-and-retention.md."""
    builds = data["builds"]
    n = len(builds)
    first_id = builds[0]["id"]
    last_id = builds[-1]["id"]

    avg_archive_mib = sum(b["archive_mib"] for b in builds) / n
    avg_unpacked_mib = sum(b["unpacked_mib"] for b in builds) / n
    single_disk_mb = builds[0]["disk_mb"]
    file_count = builds[0]["file_count"]

    deltas = data.get("deltas", [])
    if deltas:
        min_same = min(d["identical_files"] for d in deltas)
        max_same = max(d["identical_files"] for d in deltas)
        avg_same_pct = round(sum(d["identical_pct"] for d in deltas) / len(deltas))
        avg_same_mib = round(sum(d["identical_mib"] for d in deltas) / len(deltas))
        min_chg = min(d["changed_files"] for d in deltas)
        max_chg = max(d["changed_files"] for d in deltas)
        min_chg_mib = round(min(d["changed_mib"] for d in deltas))
        max_chg_mib = round(max(d["changed_mib"] for d in deltas))
        delta_bullet = (
            f"- Between consecutive daily builds, **~{avg_same_pct}% of files "
            f"({min_same:,}–{max_same:,} files, ~{avg_same_mib} MiB)** are byte-for-byte identical, "
            f"while only **{min_chg:,}–{max_chg:,} files ({min_chg_mib}–{max_chg_mib} MiB)** actually change."
        )
    else:
        delta_bullet = "- Consecutive daily builds share ~80–85% byte-identical files."

    # Incremental builds (2..N) for daily update averages
    inc_builds = builds[1:] if n > 1 else builds
    avg_base_daily_s = sum(b["base_total_s"] for b in inc_builds) / len(inc_builds)
    avg_cons_daily_s = sum(b["consumer_s"] for b in inc_builds) / len(inc_builds)
    daily_speedup = avg_base_daily_s / avg_cons_daily_s if avg_cons_daily_s > 0 else 0.0

    total_base_s = sum(b["base_total_s"] for b in builds)
    total_prod_s = sum(b["producer_s"] for b in builds)
    total_cons_s = sum(b["consumer_s"] for b in builds)
    total_speedup = total_base_s / total_cons_s if total_cons_s > 0 else 0.0

    avg_cons_daily_mib = sum(b["consumer_mib"] for b in inc_builds) / len(inc_builds)
    traffic_reduction_pct = (
        (1.0 - (avg_cons_daily_mib / avg_archive_mib)) * 100.0
        if avg_archive_mib > 0
        else 0.0
    )

    total_unpacked_disk_mb = round(n * single_disk_mb)
    single_archive_mb = round(avg_archive_mib)
    total_archive_mb = round(n * single_archive_mb)
    server_used_mb = round(data["server_used_mb"])
    server_used_str = data["server_used_str"]

    unpacked_savings_pct = (
        (1.0 - (server_used_mb / total_unpacked_disk_mb)) * 100.0
        if total_unpacked_disk_mb > 0
        else 0.0
    )
    dedup_ratio = (
        total_unpacked_disk_mb / server_used_mb if server_used_mb > 0 else 0.0
    )
    archive_savings_pct = (
        (1.0 - (server_used_mb / total_archive_mb)) * 100.0
        if total_archive_mb > 0
        else 0.0
    )

    lines = [
        f"## Real-world benchmark: Eclipse SDK builds",
        f"",
        f"To measure end-to-end transfer speed and server-side storage deduplication on real software distributions, we benchmarked **{n} consecutive daily [Eclipse SDK integration builds (`I-builds`)](https://download.eclipse.org/eclipse/downloads/)** (`linux-gtk-x86_64`, `{first_id}` through `{last_id}`):",
        f"- Each build archive is **{avg_archive_mib:.1f} MiB** compressed (`.tar.gz`) and unpacks to **{avg_unpacked_mib:.1f} MiB** (**{round(single_disk_mb)}M** on disk across **{file_count:,} files**, mostly OSGi `.jar` bundles and native libraries).",
        delta_bullet,
        f"- **Test environment:** Linux `x86_64` client and remote storage server (`{data['host']}:{data['path']}`) with `rsync -c` (`--checksum`) enabled.",
        f"",
        f"### Headline comparison ({n} daily builds)",
        f"",
        f"| Metric | Traditional (`curl` + `tar -xzf`) | `git-drev` (`select` + `get` / `put`) | Improvement |",
        f"| :--- | :--- | :--- | :--- |",
        f"| **Avg. daily update time (Consumer)** | `{avg_base_daily_s:.2f}s` (download + extract) | **`{avg_cons_daily_s:.2f}s`** (`git drev select` + `get`) | **{daily_speedup:.1f}x faster** |",
        f"| **Total sync time across all {n} builds** | `{total_base_s:.2f}s` ({n} full downloads) | **`{total_cons_s:.2f}s`** (1 cold + {n - 1} incremental) | **{total_speedup:.1f}x faster** |",
        f"| **Avg. network payload per daily update** | `{avg_archive_mib:.2f} MiB` (full `.tar.gz`) | **`{avg_cons_daily_mib:.2f} MiB`** (changed files/deltas only) | **{traffic_reduction_pct:.1f}% less traffic** |",
        f"| **Server disk space for {n} unpacked builds** | `{total_unpacked_disk_mb:,} MB` (`{n} × {round(single_disk_mb)} MB` unpacked) | **`{server_used_mb:,} MB`** (`cp -al` hardlinked storage) | **{unpacked_savings_pct:.1f}% less storage (`{dedup_ratio:.2f}x` dedup)** |",
        f"| **Server disk space vs. {n} `.tar.gz` archives** | `{total_archive_mb:,} MB` (`{n} × {single_archive_mb} MB` compressed) | **`{server_used_mb:,} MB`** (immediately usable directories) | **{archive_savings_pct:.1f}% less storage than `.tar.gz`** |",
        f"",
        f"### Per-build breakdown",
        f"",
        f"| Build ID | Unpacked size (files) | Baseline: Download + Extract | Producer: `git drev put` (sent) | Consumer: `git drev select` + `get` (received) | Consumer speedup |",
        f"| :--- | :--- | :--- | :--- | :--- | :--- |",
    ]

    for idx, b in enumerate(builds):
        tag = "*(Cold)*" if idx == 0 else "*(Delta)*"
        b_speedup = b["base_total_s"] / b["consumer_s"] if b["consumer_s"] > 0 else 0.0
        lines.append(
            f"| `{b['id']}` {tag} | `{b['unpacked_mib']:.1f} MiB` ({b['file_count']:,}) | "
            f"`{b['dl_s']:.2f}s` + `{b['ext_s']:.2f}s` = **`{b['base_total_s']:.2f}s`** | "
            f"`{b['producer_s']:.2f}s` (`{b['producer_mib']:.1f} MiB`, {b['producer_files']:,} files) | "
            f"**`{b['consumer_s']:.2f}s`** (`{b['consumer_mib']:.1f} MiB`, {b['consumer_files']:,} files) | "
            f"**{b_speedup:.1f}x faster** |"
        )

    tot_unpacked_mib = sum(b["unpacked_mib"] for b in builds)
    tot_files = sum(b["file_count"] for b in builds)
    tot_archive_mib = sum(b["archive_mib"] for b in builds)
    tot_prod_mib = sum(b["producer_mib"] for b in builds)
    tot_cons_mib = sum(b["consumer_mib"] for b in builds)

    lines.extend(
        [
            f"| **Total / Cumulative** | **`{tot_unpacked_mib:,.1f} MiB`** ({tot_files:,}) | "
            f"**`{total_base_s:.2f}s`** (`{tot_archive_mib:,.1f} MiB` `.tar.gz`) | "
            f"**`{total_prod_s:.2f}s`** (`{tot_prod_mib:,.1f} MiB` total sent) | "
            f"**`{total_cons_s:.2f}s`** (`{tot_cons_mib:,.1f} MiB` total received) | "
            f"**{total_speedup:.1f}x faster** |",
            f"",
            f"### Remote storage deduplication (`git drev usage -a`)",
            f"",
            f"Even though all {n} revisions are stored as full, unpacked **{round(single_disk_mb)}M** directory trees on the remote SSH server, `cp -al` hardlink cloning deduplicates unchanged files across revisions so the entire {n}-build history consumes only **{server_used_str}** on disk:",
            f"",
            f"```text",
            f"$ git drev usage -a",
            data["usage_output"].rstrip(),
            f"```",
            f"",
        ]
    )
    return "\n".join(lines)


if self_test:
    log("Running deterministic formula & report verification self-test...")
    sample_data = {
        "host": "drs",
        "path": "/volume1/storage_drs/data/git-drev-benchmark",
        "server_used_mb": 483.0,
        "server_used_str": "483M",
        "usage_output": (
            "Summary:\n"
            "  Remote storage:    drs:/volume1/storage_drs/data/git-drev-benchmark\n"
            "  Total space used:  483M\n"
            "  Revisions found:   5\n"
        ),
        "deltas": [
            {"identical_files": 1091, "identical_pct": 80.0, "identical_mib": 262.0, "changed_files": 272, "changed_mib": 31.7},
            {"identical_files": 1108, "identical_pct": 81.0, "identical_mib": 262.0, "changed_files": 260, "changed_mib": 33.5},
            {"identical_files": 1124, "identical_pct": 82.0, "identical_mib": 262.0, "changed_files": 244, "changed_mib": 19.1},
            {"identical_files": 1091, "identical_pct": 81.0, "identical_mib": 262.0, "changed_files": 277, "changed_mib": 56.3},
        ],
        "builds": [
            {"id": "I20261015-2300", "archive_mib": 272.98, "unpacked_mib": 302.68, "disk_mb": 306.0, "file_count": 1368, "dl_s": 19.10, "ext_s": 0.98, "base_total_s": 20.08, "producer_s": 5.77, "producer_mib": 302.9, "producer_files": 1368, "consumer_s": 4.84, "consumer_mib": 302.9, "consumer_files": 1368},
            {"id": "I20261016-2300", "archive_mib": 272.99, "unpacked_mib": 302.68, "disk_mb": 306.0, "file_count": 1368, "dl_s": 34.89, "ext_s": 1.01, "base_total_s": 35.90, "producer_s": 2.40, "producer_mib": 31.7, "producer_files": 272, "consumer_s": 2.15, "consumer_mib": 31.7, "consumer_files": 272},
            {"id": "I20261017-2300", "archive_mib": 272.99, "unpacked_mib": 302.68, "disk_mb": 306.0, "file_count": 1368, "dl_s": 14.68, "ext_s": 0.99, "base_total_s": 15.67, "producer_s": 2.41, "producer_mib": 33.5, "producer_files": 260, "consumer_s": 2.16, "consumer_mib": 33.5, "consumer_files": 260},
            {"id": "I20261018-2300", "archive_mib": 272.99, "unpacked_mib": 302.68, "disk_mb": 306.0, "file_count": 1368, "dl_s": 37.41, "ext_s": 0.99, "base_total_s": 38.40, "producer_s": 2.17, "producer_mib": 19.1, "producer_files": 244, "consumer_s": 2.03, "consumer_mib": 19.1, "consumer_files": 244},
            {"id": "I20261019-2300", "archive_mib": 272.99, "unpacked_mib": 302.68, "disk_mb": 306.0, "file_count": 1368, "dl_s": 12.74, "ext_s": 0.97, "base_total_s": 13.71, "producer_s": 2.71, "producer_mib": 56.3, "producer_files": 277, "consumer_s": 2.43, "consumer_mib": 56.2, "consumer_files": 277},
        ],
    }
    report_md = render_report(sample_data)
    assert "11.8x faster" in report_md, "Expected 11.8x daily consumer speedup"
    assert "9.1x faster" in report_md, "Expected 9.1x total speedup"
    assert "87.1% less traffic" in report_md, "Expected 87.1% traffic reduction"
    assert "68.4% less storage (`3.17x` dedup)" in report_md, "Expected 68.4% / 3.17x dedup"
    assert "64.6% less storage than `.tar.gz`" in report_md, "Expected 64.6% archive savings"
    print(report_md)
    log("Self-test passed: all formulas match docs/user-guide/storage-and-retention.md!")
    sys.exit(0)


def discover_builds() -> list[str]:
    if builds_csv.strip():
        items = [x.strip() for x in builds_csv.split(",") if x.strip()]
        if len(items) < 2:
            raise RuntimeError("Please provide at least 2 build IDs in --builds.")
        return items

    log("Discovering recent Eclipse SDK I-builds from download.eclipse.org...")
    req = urllib.request.Request(
        "https://download.eclipse.org/eclipse/downloads/",
        headers={"User-Agent": "git-drev-benchmark/1.0"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        html = resp.read().decode("utf-8", errors="replace")

    candidates = sorted(set(re.findall(r"I20[0-9]{6}-[0-9]{4}", html)))
    if len(candidates) < 5:
        raise RuntimeError(
            f"Found only {len(candidates)} I-builds on download.eclipse.org: {candidates}"
        )

    # Verify tarball availability for the most recent candidates and pick 5 consecutive builds
    valid = []
    for bid in reversed(candidates):
        url = f"https://download.eclipse.org/eclipse/downloads/drops4/{bid}/eclipse-SDK-{bid}-linux-gtk-x86_64.tar.gz"
        head_req = urllib.request.Request(
            url, method="HEAD", headers={"User-Agent": "git-drev-benchmark/1.0"}
        )
        try:
            with urllib.request.urlopen(head_req, timeout=15) as r:
                if r.status == 200:
                    valid.append(bid)
                    if len(valid) == 5:
                        break
        except Exception:
            continue

    if len(valid) < 5:
        raise RuntimeError(
            f"Could only verify {len(valid)} downloadable linux-gtk-x86_64 I-builds: {valid}"
        )
    valid.reverse()
    log(f"Selected 5 consecutive I-builds: {', '.join(valid)}")
    return valid


def tree_file_hashes(root_dir: str) -> dict[str, tuple[int, str]]:
    """Return relative_path -> (size_bytes, sha256_hex) for all regular files in root_dir."""
    result = {}
    for dirpath, _, filenames in os.walk(root_dir):
        for fname in filenames:
            full = os.path.join(dirpath, fname)
            if os.path.islink(full) or not os.path.isfile(full):
                continue
            rel = os.path.relpath(full, root_dir)
            h = hashlib.sha256()
            with open(full, "rb") as f:
                while chunk := f.read(65536):
                    h.update(chunk)
            result[rel] = (os.path.getsize(full), h.hexdigest())
    return result


def dir_stats(root_dir: str) -> tuple[float, float, int]:
    """Return (unpacked_mib, disk_mb, file_count) for root_dir."""
    du_b = subprocess.check_output(["du", "-sb", root_dir], text=True).split()[0]
    unpacked_mib = int(du_b) / (1024.0 * 1024.0)
    du_m = subprocess.check_output(["du", "-sm", root_dir], text=True).split()[0]
    disk_mb = float(du_m)
    file_count = 0
    for dirpath, _, filenames in os.walk(root_dir):
        for fname in filenames:
            full = os.path.join(dirpath, fname)
            if not os.path.islink(full) and os.path.isfile(full):
                file_count += 1
    return unpacked_mib, disk_mb, file_count


def main() -> None:
    build_ids = discover_builds()
    work_root = tempfile.mkdtemp(prefix="git-drev-bench-")
    dl_dir = cache_dir if cache_dir else os.path.join(work_root, "archives")
    os.makedirs(dl_dir, exist_ok=True)

    try:
        if clean_before:
            log(f"Cleaning remote benchmark path '{ssh_host}:{remote_path}'...")
            subprocess.run(
                ["ssh", "-T", ssh_host, f"rm -rf -- '{remote_path}' && mkdir -p -- '{remote_path}'"],
                check=True,
            )
        else:
            subprocess.run(
                ["ssh", "-T", ssh_host, f"mkdir -p -- '{remote_path}'"],
                check=True,
            )

        # Initialize local bare origin, producer repo, and consumer repo
        origin_git = os.path.join(work_root, "origin.git")
        producer_dir = os.path.join(work_root, "producer")
        consumer_dir = os.path.join(work_root, "consumer")
        extract_base = os.path.join(work_root, "extracted")
        os.makedirs(extract_base, exist_ok=True)

        subprocess.run(["git", "init", "--bare", "-b", "main", origin_git], check=True, stdout=subprocess.DEVNULL)
        os.makedirs(producer_dir, exist_ok=True)

        log("Initializing Producer metadata repository with 'git-drev init'...")
        subprocess.run(
            [
                drev_bin,
                "init",
                "-y",
                "--host",
                ssh_host,
                "--path",
                remote_path,
                "--workdir",
                "data",
                "--branch",
                "main",
            ],
            cwd=producer_dir,
            check=True,
        )

        # Enable rsync -c (--checksum) in .git-drev.yaml so unpacked tarballs preserve cp -al hardlinks
        cfg_path = os.path.join(producer_dir, ".git-drev.yaml")
        with open(cfg_path, "r", encoding="utf-8") as f:
            cfg_text = f.read()
        cfg_text = cfg_text.replace("  # rsync:\n  #   get: -c\n  #   put: -c", "  rsync:\n    get: -c\n    put: -c")
        with open(cfg_path, "w", encoding="utf-8") as f:
            f.write(cfg_text)

        subprocess.run(["git", "add", ".git-drev.yaml"], cwd=producer_dir, check=True)
        subprocess.run(["git", "commit", "--amend", "--no-edit"], cwd=producer_dir, check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["git", "remote", "add", "origin", origin_git], cwd=producer_dir, check=True)
        subprocess.run([drev_bin, "create", "main"], cwd=producer_dir, check=True)

        builds_data = []
        deltas_data = []
        prev_hashes = None
        uuids = []

        # Phase 1 & 2: Baseline (Download + Extract) & Producer (put --stats)
        for idx, bid in enumerate(build_ids, start=1):
            tar_name = f"eclipse-SDK-{bid}-linux-gtk-x86_64.tar.gz"
            tar_path = os.path.join(dl_dir, tar_name)
            time_meta_path = tar_path + ".dltime"

            # Mirror redirector with r=1 selects closest mirror; fallback to direct download.eclipse.org
            mirror_url = f"https://www.eclipse.org/downloads/download.php?file=/eclipse/downloads/drops4/{bid}/{tar_name}&r=1"

            if os.path.isfile(tar_path) and os.path.isfile(time_meta_path):
                log(f"[{idx}/{len(build_ids)}] Using cached archive for {bid}...")
                with open(time_meta_path, "r", encoding="utf-8") as tf:
                    dl_s = float(tf.read().strip())
            else:
                log(f"[{idx}/{len(build_ids)}] Downloading {tar_name} via curl...")
                t0 = time.monotonic()
                subprocess.run(
                    ["curl", "-fSL", "--retry", "3", "-o", tar_path, mirror_url],
                    check=True,
                )
                dl_s = time.monotonic() - t0
                with open(time_meta_path, "w", encoding="utf-8") as tf:
                    tf.write(f"{dl_s:.4f}\n")

            archive_mib = os.path.getsize(tar_path) / (1024.0 * 1024.0)

            # Measure tar -xzf extraction
            build_extract_dir = os.path.join(extract_base, bid)
            shutil.rmtree(build_extract_dir, ignore_errors=True)
            os.makedirs(build_extract_dir, exist_ok=True)

            log(f"[{idx}/{len(build_ids)}] Extracting {tar_name}...")
            t0 = time.monotonic()
            subprocess.run(["tar", "-xzf", tar_path, "-C", build_extract_dir], check=True)
            ext_s = time.monotonic() - t0

            unpacked_mib, disk_mb, file_count = dir_stats(build_extract_dir)
            curr_hashes = tree_file_hashes(build_extract_dir)

            if prev_hashes is not None:
                same_files = 0
                same_bytes = 0
                chg_files = 0
                chg_bytes = 0
                for rel, (sz, digest) in curr_hashes.items():
                    if rel in prev_hashes and prev_hashes[rel][1] == digest:
                        same_files += 1
                        same_bytes += sz
                    else:
                        chg_files += 1
                        chg_bytes += sz
                deltas_data.append(
                    {
                        "identical_files": same_files,
                        "identical_pct": (same_files / len(curr_hashes) * 100.0) if curr_hashes else 0.0,
                        "identical_mib": same_bytes / (1024.0 * 1024.0),
                        "changed_files": chg_files,
                        "changed_mib": chg_bytes / (1024.0 * 1024.0),
                    }
                )
            prev_hashes = curr_hashes

            # Populate Producer data/ directory
            prod_data_dir = os.path.join(producer_dir, "data")
            shutil.rmtree(prod_data_dir, ignore_errors=True)
            shutil.move(build_extract_dir, prod_data_dir)

            log(f"[{idx}/{len(build_ids)}] Producer: running 'git drev put -s {idx} --stats' for {bid}...")
            t0 = time.monotonic()
            put_proc = subprocess.run(
                [drev_bin, "put", "-s", str(idx), "--stats"],
                cwd=producer_dir,
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            prod_s = time.monotonic() - t0
            combined_put = put_proc.stdout + "\n" + put_proc.stderr
            prod_files, prod_mib = parse_rsync_wire_bytes(combined_put, is_upload=True)

            # Extract commit UUID from HEAD
            subj = subprocess.check_output(
                ["git", "log", "-1", "--format=%s"], cwd=producer_dir, text=True
            ).strip()
            rev_meta = json.loads(subj)
            uuids.append(rev_meta["uuid"])

            builds_data.append(
                {
                    "id": bid,
                    "uuid": rev_meta["uuid"],
                    "archive_mib": archive_mib,
                    "unpacked_mib": unpacked_mib,
                    "disk_mb": disk_mb,
                    "file_count": file_count,
                    "dl_s": dl_s,
                    "ext_s": ext_s,
                    "base_total_s": dl_s + ext_s,
                    "producer_s": prod_s,
                    "producer_mib": prod_mib,
                    "producer_files": prod_files,
                    "consumer_s": 0.0,
                    "consumer_mib": 0.0,
                    "consumer_files": 0,
                }
            )

        # Phase 3: Consumer (git clone + 5x select & get --stats)
        log("Cloning Consumer metadata repository from bare origin...")
        subprocess.run(["git", "clone", origin_git, consumer_dir], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(
            ["git", "config", "--local", "--add", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"],
            cwd=consumer_dir,
            check=True,
        )

        for idx, b in enumerate(builds_data, start=1):
            log(f"[{idx}/{len(builds_data)}] Consumer: running 'git drev select {b['uuid']} && git drev get --stats' ({b['id']})...")
            t0 = time.monotonic()
            subprocess.run(
                [drev_bin, "select", b["uuid"]],
                cwd=consumer_dir,
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            get_proc = subprocess.run(
                [drev_bin, "get", "--stats"],
                cwd=consumer_dir,
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            cons_s = time.monotonic() - t0
            combined_get = get_proc.stdout + "\n" + get_proc.stderr
            cons_files, cons_mib = parse_rsync_wire_bytes(combined_get, is_upload=False)
            b["consumer_s"] = cons_s
            b["consumer_mib"] = cons_mib
            b["consumer_files"] = cons_files

        # Return Consumer to main branch and run git drev usage -a
        subprocess.run([drev_bin, "select", "main"], cwd=consumer_dir, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        log("Querying remote storage deduplication with 'git drev usage -a'...")
        usage_proc = subprocess.run(
            [drev_bin, "usage", "-a"],
            cwd=consumer_dir,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        usage_out = usage_proc.stdout
        m_used = re.search(r"Total space used:\s*([0-9.]+[KMGTPE]?B?)", usage_out, re.IGNORECASE)
        server_used_str = m_used.group(1) if m_used else "0M"
        server_used_mb = parse_size_to_mb(server_used_str)

        report_payload = {
            "host": ssh_host,
            "path": remote_path,
            "server_used_mb": server_used_mb,
            "server_used_str": server_used_str,
            "usage_output": usage_out,
            "deltas": deltas_data,
            "builds": builds_data,
        }

        report_md = render_report(report_payload)
        if output_file:
            with open(output_file, "w", encoding="utf-8") as out_f:
                out_f.write(report_md)
            log(f"Wrote Markdown benchmark report to '{output_file}'")

        print(report_md)

        if clean_after:
            log(f"Cleaning up remote benchmark directory '{ssh_host}:{remote_path}'...")
            subprocess.run(["ssh", "-T", ssh_host, f"rm -rf -- '{remote_path}'"], check=True)

    finally:
        if not cache_dir:
            shutil.rmtree(work_root, ignore_errors=True)
        else:
            # Keep cache_dir if outside work_root, remove work_root
            if not os.path.abspath(dl_dir).startswith(os.path.abspath(work_root)):
                shutil.rmtree(work_root, ignore_errors=True)


if __name__ == "__main__":
    main()
PYEOF
