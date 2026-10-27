#!/usr/bin/env python3
"""Scrub raw Terminus JSON captures into test fixtures.

Usage:
    scrub_fixtures.py RAW_DIR OUT_DIR [--rename OLD=NEW ...]

RAW_DIR holds output captured from *read-only* Terminus commands, one file
per command, named after the fixture it becomes, for example:

    terminus site:list --format=json            > site_list.json
    terminus env:list SITE --format=json --fields='*' > env_list_SITE.json
    terminus env:code-log SITE.ENV --format=json > code_log_SITE_ENV.json
    terminus workflow:list SITE --format=json    > workflow_list_SITE.json
    terminus list --format=json                  > list.json

Every .json file is scrubbed: personal names in author/user fields become
"Developer A/B/…", emails become userN@example.com, and site/org/owner
UUIDs are replaced with stable fake ones (workflow and operation IDs are
kept). Long lists are trimmed, keeping any failed workflows. .txt and
.stderr files get the same text replacements. list.json (the command
catalog) is copied verbatim.

--rename OLD=NEW replaces OLD with NEW in file contents and file names,
for site names, multidev names, ticket prefixes, org names, or anything
else that shouldn't appear in a public repo. Longer OLD strings are
applied first. Never capture connection:info: it contains passwords.
"""
import argparse
import hashlib
import json
import pathlib
import re
import uuid

ap = argparse.ArgumentParser()
ap.add_argument("raw", type=pathlib.Path)
ap.add_argument("out", type=pathlib.Path)
ap.add_argument("--rename", action="append", default=[], metavar="OLD=NEW")
args = ap.parse_args()
renames = sorted((r.split("=", 1) for r in args.rename), key=lambda p: -len(p[0]))
args.out.mkdir(parents=True, exist_ok=True)

KEEP_PEOPLE = {"Pantheon", "Pantheon Automation", "automated"}
LIMITS = {"code_log_": 15, "workflow_list_": 30, "backup_list_": 9}
people: dict[str, str] = {}
emails: dict[str, str] = {}
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
UUID = re.compile(r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b")
keep_ids: set[str] = set()


def fake_uuid(s: str) -> str:
    return str(uuid.UUID(hashlib.sha256(s.encode()).hexdigest()[:32]))


def person(name: str) -> str:
    if name in KEEP_PEOPLE:
        return name
    if name not in people:
        people[name] = f"Developer {chr(ord('A') + len(people))}"
    return people[name]


def email(m: re.Match[str]) -> str:
    e = m.group(0)
    if e not in emails:
        emails[e] = f"user{len(emails) + 1}@example.com"
    return emails[e]


def rename(s: str) -> str:
    for old, new in renames:
        s = s.replace(old, new)
    return s


def scrub_str(s: str) -> str:
    s = EMAIL.sub(email, s)
    s = UUID.sub(lambda m: m.group(0) if m.group(0) in keep_ids else fake_uuid(m.group(0)), s)
    return rename(s)


def scrub(v, key=""):
    if isinstance(v, dict):
        return {scrub_str(k): scrub(x, k) for k, x in v.items()}
    if isinstance(v, list):
        return [scrub(x, key) for x in v]
    if isinstance(v, str):
        if key in ("author", "user") and not EMAIL.fullmatch(v):
            v = person(v)
        return scrub_str(v)
    return v


def collect_ids(v):
    if isinstance(v, dict):
        for k, x in v.items():
            if UUID.fullmatch(k):
                keep_ids.add(k)
            collect_ids(x)


raw_files = sorted(args.raw.iterdir())
loaded = {}
for f in raw_files:
    if f.suffix != ".json" or f.name == "list.json" or f.name.startswith("connection_info"):
        continue
    data = json.loads(f.read_text())
    if f.name.startswith(("workflow_", "wf_")):
        collect_ids(data)
    limit = next((n for p, n in LIMITS.items() if f.name.startswith(p)), None)
    if limit:
        trimmed = dict(list(data.items())[:limit]) if isinstance(data, dict) else data[:limit]
        if f.name.startswith("workflow_list_") and isinstance(data, dict):
            for k, v in data.items():  # keep failures even if they fall outside the limit
                if v.get("status") == "failed":
                    trimmed[k] = v
        data = trimmed
    loaded[f.name] = data

for name, data in loaded.items():
    (args.out / rename(name)).write_text(json.dumps(scrub(data), indent=4) + "\n")
n = len(loaded)
for f in raw_files:
    if f.suffix in (".txt", ".stderr"):
        (args.out / rename(f.name)).write_text(scrub_str(f.read_text()))
        n += 1
if (args.raw / "list.json").exists():
    (args.out / "list.json").write_text((args.raw / "list.json").read_text())
    n += 1
print(f"wrote {n} fixtures to {args.out}; people={len(people)} emails={len(emails)}")
