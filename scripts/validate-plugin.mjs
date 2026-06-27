#!/usr/bin/env node
// Guardrail for the e2a agent plugin (plugins/e2a) and its marketplace
// manifests. A malformed manifest or skill silently fails to load in the
// target client (Claude Code / Codex / Cursor) with no build error — this
// script is the gate that turns those into a CI failure instead.
//
// It checks, dependency-free:
//   1. Every marketplace.json + plugin.json parses and carries required fields.
//   2. The plugin version is identical across all client manifests and the
//      marketplace metadata (the .claude-plugin plugin.json is the source of
//      truth) — so a bump can't land in one manifest and skew the others.
//   3. Every marketplace `source` points at a real plugin directory.
//   4. Each skill's SKILL.md frontmatter satisfies Claude Code's constraints:
//      `name` present, matches its directory, lowercase/digits/hyphens, ≤64
//      chars; `description` present, ≤1024 chars.
//   5. Each manifest's referenced icon/logo file exists on disk.
//
// Run by the `plugin` job in .github/workflows/test.yml.

import { readFileSync, existsSync, readdirSync, statSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const PLUGIN_DIR = join(ROOT, "plugins", "e2a");

// Claude Code skill-name rules: lowercase letters, digits, hyphens; ≤64 chars.
const NAME_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const MAX_NAME = 64;
// Claude Code budgets name + description together; stay well under.
const MAX_DESCRIPTION = 1024;

const FRONTMATTER_RE = /^---\r?\n([\s\S]*?)\r?\n---/;

const errors = [];
const fail = (msg) => errors.push(msg);

const rel = (p) => p.slice(ROOT.length + 1);

function readJSON(absPath) {
  try {
    return JSON.parse(readFileSync(absPath, "utf8"));
  } catch (err) {
    fail(`${rel(absPath)}: ${err.message}`);
    return null;
  }
}

// --- 1 + 2: plugin manifests + version consistency ---------------------------

// The .claude-plugin manifest is the version source of truth.
const claudeManifestPath = join(PLUGIN_DIR, ".claude-plugin", "plugin.json");
const claudeManifest = readJSON(claudeManifestPath);
const canonicalVersion = claudeManifest?.version;

if (!canonicalVersion || typeof canonicalVersion !== "string") {
  fail(`${rel(claudeManifestPath)}: missing string "version"`);
}

// Every client plugin manifest: { file, dottedVersionKey, requiredFields }.
const PLUGIN_MANIFESTS = [
  { file: claudeManifestPath, versionKey: "version" },
  { file: join(PLUGIN_DIR, ".codex-plugin", "plugin.json"), versionKey: "version" },
  { file: join(PLUGIN_DIR, ".cursor-plugin", "plugin.json"), versionKey: "version" },
];

// Marketplace manifests across clients: { file, source-relative-to-repo, version? }.
const MARKETPLACE_MANIFESTS = [
  { file: join(ROOT, ".claude-plugin", "marketplace.json"), versionKey: "metadata.version", versionOptional: true },
  { file: join(ROOT, ".cursor-plugin", "marketplace.json"), versionKey: "metadata.version" },
  { file: join(ROOT, ".agents", "plugins", "marketplace.json"), versionKey: null },
];

const getAt = (obj, key) => key.split(".").reduce((o, k) => (o == null ? o : o[k]), obj);

for (const { file, versionKey } of PLUGIN_MANIFESTS) {
  const m = readJSON(file);
  if (!m) continue;
  for (const field of ["name", "description", "version"]) {
    if (!m[field]) fail(`${rel(file)}: missing required field "${field}"`);
  }
  const v = getAt(m, versionKey);
  if (canonicalVersion && v !== canonicalVersion) {
    fail(`${rel(file)}: version "${v}" != canonical "${canonicalVersion}" (.claude-plugin is source of truth)`);
  }
  // Icon/logo references must resolve.
  for (const iconKey of ["icon", "logo"]) {
    const ref = m[iconKey] ?? m.interface?.composerIcon;
    if (ref) {
      const abs = join(dirname(file), "..", ref.replace(/^\.\//, ""));
      // manifests live in plugins/e2a/<client-dir>/; icon path is plugin-root-relative.
      const iconAbs = join(PLUGIN_DIR, ref.replace(/^\.\//, ""));
      if (!existsSync(iconAbs) && !existsSync(abs)) {
        fail(`${rel(file)}: ${iconKey} "${ref}" not found`);
      }
    }
  }
}

// --- 3: marketplace manifests parse, sources resolve, versions consistent ----

for (const { file, versionKey, versionOptional } of MARKETPLACE_MANIFESTS) {
  if (!existsSync(file)) {
    fail(`missing marketplace manifest: ${rel(file)}`);
    continue;
  }
  const m = readJSON(file);
  if (!m) continue;
  if (!Array.isArray(m.plugins) || m.plugins.length === 0) {
    fail(`${rel(file)}: "plugins" must be a non-empty array`);
    continue;
  }
  for (const p of m.plugins) {
    // source is either a string path or { source, path }.
    const srcPath = typeof p.source === "string" ? p.source : p.source?.path;
    if (!srcPath) {
      fail(`${rel(file)}: plugin "${p.name}" has no source path`);
      continue;
    }
    const abs = join(ROOT, srcPath.replace(/^\.\//, ""));
    if (!existsSync(abs) || !statSync(abs).isDirectory()) {
      fail(`${rel(file)}: plugin "${p.name}" source "${srcPath}" is not a directory`);
    }
  }
  if (versionKey) {
    const v = getAt(m, versionKey);
    if (versionOptional && v === undefined) continue;
    if (canonicalVersion && v !== canonicalVersion) {
      fail(`${rel(file)}: ${versionKey} "${v}" != canonical "${canonicalVersion}"`);
    }
  }
}

// --- 4: SKILL.md frontmatter -------------------------------------------------

const skillsDir = join(PLUGIN_DIR, "skills");
const skillDirs = existsSync(skillsDir)
  ? readdirSync(skillsDir, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name)
  : [];

if (skillDirs.length === 0) fail(`no skills found under ${rel(skillsDir)}`);

for (const dir of skillDirs) {
  const file = join(skillsDir, dir, "SKILL.md");
  if (!existsSync(file)) {
    fail(`${dir}: missing SKILL.md`);
    continue;
  }
  const match = readFileSync(file, "utf8").match(FRONTMATTER_RE);
  if (!match) {
    fail(`${dir}: missing YAML frontmatter (--- ... ---)`);
    continue;
  }
  // Minimal top-level "key: value" parse — sufficient for name/description.
  const fm = {};
  for (const line of match[1].split(/\r?\n/)) {
    const m = line.match(/^([A-Za-z0-9_-]+):\s*(.*)$/);
    if (m) fm[m[1]] = m[2].trim();
  }

  const { name, description } = fm;
  if (!name) {
    fail(`${dir}: SKILL.md frontmatter missing "name"`);
  } else {
    if (name !== dir) fail(`${dir}: skill name "${name}" must match its directory`);
    if (!NAME_RE.test(name)) fail(`${dir}: name "${name}" must be lowercase letters, digits, and hyphens`);
    if (name.length > MAX_NAME) fail(`${dir}: name "${name}" exceeds ${MAX_NAME} chars`);
  }
  if (!description) {
    fail(`${dir}: SKILL.md frontmatter missing "description"`);
  } else if (description.length > MAX_DESCRIPTION) {
    fail(`${dir}: description is ${description.length} chars (max ${MAX_DESCRIPTION})`);
  }
}

// --- 5: standalone .mcp.json -------------------------------------------------

const mcpPath = join(PLUGIN_DIR, ".mcp.json");
const mcp = readJSON(mcpPath);
if (mcp && (!mcp.mcpServers || Object.keys(mcp.mcpServers).length === 0)) {
  fail(`${rel(mcpPath)}: "mcpServers" must define at least one server`);
}

// --- report ------------------------------------------------------------------

if (errors.length > 0) {
  console.error(`\n✗ Plugin validation failed:\n${errors.map((e) => `  - ${e}`).join("\n")}\n`);
  process.exit(1);
}
console.log(`✓ Plugin valid: ${skillDirs.length} skill(s), version ${canonicalVersion}, manifests in sync across Claude/Codex/Cursor`);
