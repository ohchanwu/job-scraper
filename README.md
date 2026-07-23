# jobcron — 신입 IT Job Briefing

_[한국어로 보기 🇰🇷](README.ko.md)_

A calm daily job-posting briefing for Korean new-grad (신입) IT job seekers.
The primary full app will be available at `jobcron.app`: set your profile once,
then let Jobcron gather and rank the day's openings into one calm page.

Jobcron scrapes Korean job boards — the aggregators [점핏 (Jumpit)](https://jumpit.saramin.co.kr),
[랠릿 (Rallit)](https://www.rallit.com), [데모데이](https://demoday.co.kr), and
[그리팅 (Greeting)](https://greetinghr.com), plus the company career boards
[당근 (Daangn)](https://careers.daangn.com), 크래프톤, 몰로코, and 센드버드 (via Greenhouse) —
scores every new-grad IT posting against your profile, and shows a one-page daily
briefing with every match explained. (워크넷 also turns on with a free government
API key — see *Usage*.)

> **Deployment status:** The read-only demo is live at
> [demo.jobcron.app](https://demo.jobcron.app). The full production app at
> `jobcron.app` is not publicly available yet; its deployment configuration is
> ready, and launch is coming soon.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshots/dashboard-dark.png">
  <img src="docs/assets/screenshots/dashboard.png" alt="Score-sorted all-postings page with source filters and an expanded AI evaluation panel">
</picture>

## Why

신입 IT job hunting in Korea means checking a dozen portals every day while also
doing algorithm prep, resume writing, and portfolio work. Existing tools amplify
the stress — notification spam, spreadsheet exports, ATS-style scoring that feels
like surveillance.

jobcron is built for the *emotional layer*: a calm morning ritual that does
the daily grind for you. Warm colors, encouraging copy, and zero-match days that
say "천천히 가도 괜찮아요" instead of showing empty-state shame.

## What it does

- **Scrapes several Korean job boards** — 점핏, 랠릿, 데모데이, 그리팅, and the company
  boards 당근·크래프톤·몰로코·센드버드 (plus 워크넷 with a key) — for 신입 IT postings, one
  polite request per second, robots.txt respected. Cross-source duplicates collapse
  onto a single card.
- **Scores each posting** against a structured profile you fill in once: tech
  stack, career level, location, salary floor, and dealbreaker keywords — each
  category's weight adjustable.
- **Explains every score** — `React +20 · 신입 +25 · 서울 +15` — so you can see the
  algorithm working *for* you, not *on* you.
- **Keeps what matters in reach** — a 전체 공고 archive of everything ever scraped,
  북마크 for the ones you're chasing, and a 숨긴 공고 list for the ones you're not.
  Low-scoring postings fold away below a minimum-score line you set.
- **Filters by source** right on the briefing, so you can read one portal at a time —
  and the filter (and the 전체 공고 점수순/날짜순 sort) stick across pages and visits.
- **Streams the scrape live**, so the slow part becomes the interesting part.
- **Optional AI scoring (bring your own key).** Add an Anthropic, OpenAI, or Gemini API key and
  the briefing gains evidence-cited adjustments — each one backed by a real quote
  from the posting, with a daily token budget you control. Entirely optional; with
  no key the app scores exactly as before. See *AI scoring* below.
- A read-only web demo is live now, and the full `jobcron.app` experience is
  coming soon. Local operation remains available for contributors and self-hosters.

## Usage

1. On first run you land on the profile form. Fill in your stacks, location, and
   any dealbreaker keywords, then save.
2. Click **스크랩 시작** and watch the scrape stream in.
3. Read your briefing — postings sorted by fit, each score broken down.

Run it once a day. That is the whole ritual.

## Known limitations

- **Matching is AI-assisted when AI is configured.** The first AI layer reads
  career and education requirements from the posting text before scoring,
  rather than relying only on exact tokens; if AI is unavailable, deterministic
  rules take over. Explicit dealbreaker keywords remain literal filters:
  "개발" does not match "개발자", while "야근" also catches "야근 없음".
- **The briefing is today's postings.** The front page shows what was first seen
  today — the daily ritual. Everything ever scraped stays in 전체 공고 (sortable by
  date or by fit), so nothing is lost; it just isn't shouting at you each morning.
- **New-grad IT only.** Sources are queried with their 신입 / entry filters; this
  is not a general job search.
- No notifications or résumé parsing. Production collects postings once at 05:00 Asia/Seoul,
  then analyzes each profiled user sequentially through the in-process scheduler.
- Signup is access-code gated for the first cohort. Email ownership is not verified and recovery
  is operator-assisted; truly public signup needs the safeguards in the
  [archived follow-up specification](docs/superpowers/archive/2026-07-22-multi-user-account-expansion/260715-multi-user-account-expansion.md#truly-public-signup-follow-up).

## AI scoring (optional, v2.0, bring your own key)

Off by default. On the profile form, open **AI 분석 (선택)**, select **Anthropic**,
**OpenAI**, or **Gemini**, paste your own API key, and fill in a few free-text goals
(what work you like, what you want to avoid). The server encrypts your key before
storing it in PostgreSQL, binds it to your user and provider, and never shows it
again after you save it.

With AI on, a first layer reads each posting's career range, new-grad eligibility,
and education requirements before the regular score is calculated. A second layer
compares the posting with your free-text goals, so each posting can carry an
**AI 분석** chip you click to see the exact quote that justifies the adjustment —
no quote, no adjustment. A per-page **AI 평가** button re-rates the postings you're
looking at (for example after you change your goals, or to analyze more than one
scrape covered), and a daily token budget (which you set) keeps spend bounded.

This is the **v2.0** line and ships as a `-alpha` prerelease while the live AI
path gets more real-world mileage. Everything else in the app works identically
whether or not AI is configured.

## Advanced local use

Most users should use `jobcron.app` once it launches. Local binaries and source
builds remain available for contributors and self-hosters who want to run the
writable app themselves.

### Install a release binary

Download the binary for your platform from the
[latest release](https://github.com/ohchanwu/jobcron/releases/latest), unpack
it, and run it. The app opens at <http://localhost:7777>.

**macOS (Apple Silicon)**

```sh
curl -L https://github.com/ohchanwu/jobcron/releases/latest/download/jobcron_darwin_arm64.tar.gz | tar xz
./jobcron
```

**macOS (Intel)** — use `jobcron_darwin_amd64.tar.gz`.

**Linux (x86-64)**

```sh
curl -L https://github.com/ohchanwu/jobcron/releases/latest/download/jobcron_linux_amd64.tar.gz | tar xz
./jobcron
```

**Windows** — download `jobcron_windows_amd64.zip`, unzip, run `jobcron.exe`.

### First-run notes

- **macOS Gatekeeper** may block an unsigned binary. Right-click the file → Open,
  or run `xattr -d com.apple.quarantine ./jobcron`.
- **Windows SmartScreen**: choose **More info → Run anyway**.
- **Upgrading from the legacy SQLite app:** fully stop it, preserve `jobs.db`,
  its `-wal` sidecar, and the optional `ai_keys.json`, then follow the verified
  import procedure in the
  [local PostgreSQL guide](https://github.com/ohchanwu/jobcron/blob/main/deploy/local/README.md).
  Release archives include the packaged `jobcron-import`; no source checkout is
  required. Normal startup never opens or moves the legacy database.

Flags: `--port` (default `7777`), `--no-open` (do not open a browser),
`--worknet-api-key` (enable the 워크넷 source — a free key from
[data.go.kr](https://www.data.go.kr); also read from `JOBCRON_WORKNET_KEY`),
`--version`.

Writable application data lives in PostgreSQL. With no `DATABASE_URL`, ordinary
local startup manages PostgreSQL 18 through Docker Compose and preserves it in
the named `jobcron-postgres18-cluster` volume. Legacy SQLite is read only by the
explicit `jobcron-import --sqlite` migration tool.

### Interactive localhost preview

Run `scripts/preview-interactive.sh [port]` (default `17778`) to try the normal,
writable app at `http://127.0.0.1:17778` without touching your usual app data.
The launcher takes an atomic lock scoped to your user and HTTP port, refuses an
unrelated listener, and starts the shared `jobcron-local` PostgreSQL service. It
then creates a unique disposable database, private state directory, and
encryption key. The preview runs in non-production mode with scheduling disabled.

On exit it drops only that preview database and removes its private state; it
never runs Compose `down` against the shared service. Set
`JOBCRON_PREVIEW_KEEP=1` to retain both and print their exact manual cleanup
commands. Use those commands instead of bringing down the shared Compose project.

### Build from source

```sh
git clone https://github.com/ohchanwu/jobcron
cd jobcron
go build ./cmd/jobcron
```

Requires Go 1.26+. Running the JavaScript lifecycle tests also requires the
pinned CI version, Node.js 22.15.1 LTS. The shipped application remains pure Go
with no CGO or Node.js runtime dependency.

### Local PostgreSQL development

The managed local lifecycle uses Docker project `jobcron-local`, PostgreSQL 18
at `postgres://postgres@127.0.0.1:55432/jobcron_dev?sslmode=disable`, and the
named volume `jobcron-postgres18-cluster`. Docker Engine, the `docker compose`
plugin, and a running Docker daemon are required. Both ordinary no-URL startup
and the preview start or reuse this service automatically.

```sh
scripts/preview-interactive.sh
```

An explicit `DATABASE_URL` bypasses managed Docker startup and must point to a
database with exactly one existing user. Managed local startup creates its fixed
no-login owner only when the users table is empty, reuses one verified imported
owner unchanged, and refuses ambiguous multi-user state.

Compose health or container metadata alone is not enough: startup also requires
real TCP reachability at `127.0.0.1:55432`. Failures preserve containers and
volumes and print `ps` and `logs postgres` diagnostics; startup never removes
state automatically. Ordinary app shutdown also leaves PostgreSQL running.
Explicit stop, reset, and narrowly scoped malformed-container recovery commands
live in [deploy/local/README.md](deploy/local/README.md).

## Architecture and documentation

See the [architecture overview](docs/architecture.md) for the runtime, request,
scraping, scoring, AI, storage, and deployment boundaries. The
[documentation index](docs/README.md) links to detailed decisions and operator guides.

## Contributing

Issues and pull requests are welcome, especially from other Korean dev job
seekers. Parked future ideas live in [feature-ideas.md](docs/product/feature-ideas.md).

## License

MIT — see [LICENSE](LICENSE).
