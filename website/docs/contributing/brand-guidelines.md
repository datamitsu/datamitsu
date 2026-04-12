---
title: Brand Guidelines
description: Voice, tone, and style guidelines for datamitsu documentation and communications
---

# Brand Guidelines

**Your toolchain deserves a home.**

Others run tools. **datamitsu defines what those tools are.**

datamitsu is the only tool that packages and distributes complete, configured toolchains. Not a task runner. Not a version manager. **Toolchain distribution as code.**

We exist so you pay the configuration tax only once.

## Brand Personality

### Voice Attributes

**Confident, not arrogant**

- ✅ "datamitsu is a platform for building config distributions. Think lefthook, but for your entire tooling ecosystem."
- ✅ "datamitsu is for teams distributing standardized configs, not individual developers switching tool versions."
- ❌ "datamitsu is the ultimate all-in-one development tool that will revolutionize your workflow."

**Technical, not jargon-y**

- ✅ "Every binary is verified with SHA-256 hashes before extraction to prevent malicious code."
- ❌ "Enterprise-grade military-level security powered by blockchain-enabled cryptographic algorithms."

**Security-focused, not paranoid**

- ✅ "Hash verification ensures binaries match what the maintainer published."
- ❌ "Without datamitsu, hackers will definitely compromise your entire infrastructure!"

## Writing Guidelines

### Do

- **Use "Others X. datamitsu Y." framing** — "Others run tools. datamitsu defines what those tools are."
- **Lead with "Different problems" / "Different layers"** — Make distinctions clear
- **Use analogies from the ecosystem** — "Think lefthook, but for your entire tooling ecosystem"
- **Be specific about technical details** — SHA-256, not just "secure"
- **Acknowledge competitors honestly** — mise/moon solve different problems, explain how
- **Write short declarative sentences** — "Not a feature. The entire point."

### Don't

- **Use marketing buzzwords** — "Revolutionary," "game-changing," "paradigm shift"
- **Use vague security claims** — No "military-grade" or "blockchain-enabled"
- **Bash competitors** — Explain differences, never attack
- **Apologize for scope** — "datamitsu is for X, not Y" (confident, not "sorry, but...")

## Example Voice

✅ **Good:** "datamitsu is a platform for building config distributions. Think lefthook, but for your entire tooling ecosystem."

**Why:** Specific analogy, clear boundaries, confident.

❌ **Bad:** "datamitsu is a revolutionary AI-powered cloud-native DevOps solution that will transform your development workflow."

**Why:** Buzzwords, vague, overpromises.

## Language Policy

**English only. No exceptions.**

All documentation, code comments, commit messages, issues, and communications must be written in English. This policy ensures accessibility for the global developer community.

## Visual Identity

### Name Meaning

The name "datamitsu" is built on a double meaning in Japanese:

- **光 (mitsu)** — "light" → illuminating complexity, making the unclear clear
- **蜜 (mitsu)** — "honey" → smooth, polished, efficient (like a beehive)

The logo features a bee — the creature that produces honey through collective, structured work.

### Core Visual Metaphor: Light Breaking Through Complexity

**Honey + Light + Bee = datamitsu's visual identity**

- **Honey/Amber tones** — Warmth, polish, the smooth result of structured work
- **Deep blue backdrop** — The complexity being illuminated
- **Light rays** — Clarity cutting through the mess
- **Hexagons (honeycomb)** — Structure, efficiency, bee reference

### Color System

Built around the honey/light metaphor:

- **Primary: Amber/honey gold (#FFA500 family)** — The core brand color. Represents light, honey, and illumination
- **Secondary: Deep blue (#1E3A8A family)** — The backdrop. Complexity that light shines through
- **Visual pattern:** Amber light breaking through blue complexity

Not "blue for trust, gold for warmth." **Light illuminating complexity.**

### Iconography

Visual elements tied to honey/light/bee:

- **Honeycomb/hexagons** — Structure, efficiency, collective work (bee reference)
- **Light rays/beams** — Illumination, clarity breaking through
- **Layered hexagons** — Config inheritance as stacked honeycomb cells

Avoid generic dev tool icons (lock, shield, package, stack). Use the metaphor.

## Competitive Positioning Language

When discussing similar tools, use this framework:

### Template

> [Competitor] does [their use case]. **datamitsu solves a different problem.**
>
> **Different problems:**
>
> - [Competitor]: [their use case in 5-10 words]
> - datamitsu: [our use case in 5-10 words]
>
> **Use both:** [complementary example in one sentence]

### Example (mise)

> mise manages tool versions per developer. **datamitsu solves a different problem.**
>
> **Different problems:**
>
> - mise: Per-directory runtime flexibility for individuals
> - datamitsu: Team-wide toolchain standardization via packages
>
> **Use both:** mise manages Node version, datamitsu delivers ESLint with your company config.

### Example (moon)

> moon orchestrates monorepo builds with dependency graphs and caching. **datamitsu operates at a different layer.**
>
> **Different layers:**
>
> - moon: When/how tasks run (build orchestration)
> - datamitsu: What tools exist (toolchain delivery)
>
> **Use both:** datamitsu delivers the linters, moon runs them in optimal order.

## Documentation Style

### Code Examples

- **Always test examples** — Every code snippet must work
- **Use real-world scenarios** — Not "foo" and "bar"
- **Include expected output** — Show what users should see

### Formatting

- **Headings:** Sentence case ("How to configure tools")
- **Links:** Descriptive text, not "click here"
- **Admonitions:** `:::caution` for warnings, `:::danger` for destructive actions

## Core Messaging

**Tagline:** "Your toolchain deserves a home."

**One-line intro:** "datamitsu is a platform for building reproducible, security-first development tool distributions."

## Messaging by Audience

### For Platform Engineers

"Pay the configuration tax once. Distribute everywhere. Not scattered across 50 repos."

**Emphasize:** Toolchain distribution via npm/gem/pypi. Security policies (mandatory SHA-256). Version control as packages.

### For Development Teams

"Your company's toolchain in one package. No copy-paste configs. No manual setup."

**Emphasize:** Migration-free updates via patching. Override capabilities when needed. Docker-optimized CI.

### For Open Source Maintainers

"Ship the template with tools inside. Not a README telling people what to install."

**Emphasize:** Contributors get identical setup automatically. Version consistency enforced via package dependency.
