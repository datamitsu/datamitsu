package sponsor

import (
	"fmt"
	"math/rand"
)

const sponsorURL = "https://datamitsu.com/sponsor"

var rotatingMessages = []string{
	"💛 datamitsu is free, open source, and maintained by humans. Support the project → %s",
	"⏱ You just saved time on tooling. Consider sharing a bit of it back → %s",
	"🔒 No tracking. No telemetry. No paywall. Just tools that work. Sponsor datamitsu → %s",
	"🌱 datamitsu is independently built and funded by its users → %s",
	"✅ Your toolchain is configured. If datamitsu makes your life easier, consider supporting it → %s",
	"🔧 Open source doesn't mean zero cost to maintain. Help keep datamitsu going → %s",
	"☕ datamitsu runs on coffee and sponsorships. One of those you can help with → %s",
	"🤝 Every sponsor helps datamitsu stay free for everyone → %s",
	"😌 Like not configuring tools from scratch? Us too. Support datamitsu → %s",
	"🛡 datamitsu: no ads, no data collection, no vendor lock-in. Fund independent dev tools → %s",
	"💜 This tool is free because people like you sponsor it → %s",
	"🔐 datamitsu verifies every binary it downloads. Help us keep shipping secure tooling → %s",
	"📦 One package, all your tools configured. If that saves you headaches, consider giving back → %s",
	"👋 Behind datamitsu is a small team that cares about developer experience. Sponsor their work → %s",
	"🧱 Reproducible builds, verified binaries, zero telemetry. Support the project that respects your workflow → %s",
	"🚀 datamitsu just configured your toolchain in seconds. Help us keep shipping fast → %s",
	"🎯 Zero config overhead. Zero telemetry. Zero subscriptions. Sponsor independence → %s",
}

func selectRandomMessage(rnd *rand.Rand) string {
	template := rotatingMessages[rnd.Intn(len(rotatingMessages))]
	return fmt.Sprintf(template, sponsorURL)
}
