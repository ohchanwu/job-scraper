# jobcron — 신입 IT Job Briefing

_[Read this in English 🇬🇧](README.md)_

한국 신입 IT 구직자를 위한 차분한 일일 채용 공고 브리핑입니다. 전체 기능을
제공하는 기본 앱은 `jobcron.app`에서 만날 수 있게 됩니다. 프로필을 한 번
설정하면 Jobcron이 그날의 공고를 모아 순위를 매기고 한 페이지에 정리합니다.

Jobcron은 한국 채용 플랫폼 [점핏 (Jumpit)](https://jumpit.saramin.co.kr),
[랠릿 (Rallit)](https://www.rallit.com), [데모데이](https://demoday.co.kr),
[그리팅 (Greeting)](https://greetinghr.com)과 기업 채용 페이지
[당근 (Daangn)](https://careers.daangn.com), 크래프톤, 몰로코, 센드버드(Greenhouse 경유)를
스크랩합니다. 모든 신입 IT 공고를 사용자 프로필 기준으로 점수화한 뒤, 각 매칭에 대한
설명과 함께 한 페이지짜리 일일 브리핑을 보여줍니다. (워크넷은 무료 정부 API 키로
켤 수 있습니다 — *사용법* 참고.)

> **배포 상태:** 읽기 전용 데모는
> [demo.jobcron.app](https://demo.jobcron.app)에서 이용할 수 있습니다. 전체 기능을
> 제공하는 프로덕션 앱 `jobcron.app`은 아직 공개되지 않았지만 배포 준비를 마쳤으며,
> 곧 출시할 예정입니다.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshots/dashboard-dark.png">
  <img src="docs/assets/screenshots/dashboard.png" alt="점수순으로 정렬된 전체 공고 페이지의 소스 필터와 펼쳐진 AI 평가 패널">
</picture>

## 왜 만들었나 (Why)

한국에서 신입 IT 구직을 한다는 것은 알고리즘 공부, 이력서 작성, 포트폴리오 작업을
병행하면서도 매일 수십 개의 포털을 확인해야 한다는 뜻입니다. 기존 도구들은 오히려 그
스트레스를 키웁니다 — 알림 폭탄, 스프레드시트 내보내기, 마치 감시처럼 느껴지는
ATS 스타일 점수화 말입니다.

jobcron은 *감정적 측면(emotional layer)*을 위해 만들어졌습니다: 매일의 고된 작업을
대신 처리해 주는, 차분한 아침 의식(ritual)입니다. 따뜻한 색감, 격려하는 문구, 그리고 매칭이
하나도 없는 날에는 텅 빈 화면의 부끄러움을 보여주는 대신 "천천히 가도 괜찮아요"라고
말해 줍니다.

## 무엇을 하나 (What it does)

- **여러 한국 채용 출처를 스크랩합니다** — 점핏, 랠릿, 데모데이, 그리팅과 기업 채용
  페이지 당근·크래프톤·몰로코·센드버드 (키가 있으면 워크넷 추가) — 신입 IT 공고를
  대상으로, 초당 한 건의 정중한 요청으로, robots.txt를 준수하며 가져옵니다. 여러
  출처에 걸친 중복 공고는 하나의 카드로 합쳐집니다.
- **각 공고를 점수화합니다** — 한 번만 입력하면 되는 구조화된 프로필 기준으로 점수를
  매깁니다: 기술 스택, 경력 수준, 지역, 최저 희망 연봉, 그리고 제외
  키워드(dealbreaker keyword, 절대 피하고 싶은 조건). 각 항목의 가중치는 조정할 수 있습니다.
- **모든 점수를 설명합니다** — `React +20 · 신입 +25 · 서울 +15` — 알고리즘이 당신에게
  *불리하게(on)*가 아니라 *유리하게(for)* 작동하고 있음을 직접 확인할 수 있습니다.
- **중요한 것을 가까이 둡니다** — 지금까지 스크랩한 모든 것을 담은 전체 공고 보관함,
  쫓고 있는 공고를 위한 북마크, 그리고 관심 없는 공고를 위한 숨긴 공고 목록을 제공합니다.
  점수가 낮은 공고는 직접 설정한 최저 점수 기준선 아래로 접혀 들어갑니다.
- **출처별로 필터링합니다** — 브리핑 화면에서 바로 가능하므로, 한 번에 한 포털씩 읽을 수
  있습니다.
- **스크랩 과정을 실시간으로 보여줍니다** — 느린 과정이 오히려 흥미로운 과정이 됩니다.
- **선택적 AI 점수화 (당신의 키를 직접 사용).** Anthropic, OpenAI 또는 Gemini API 키를 추가하면
  브리핑에 증거 인용 기반 조정이 더해집니다 — 각 조정은 공고에서 가져온 실제 인용문으로
  뒷받침되며, 일일 토큰(token) 예산은 당신이 직접 통제합니다. 완전히 선택 사항이며, 키가
  없으면 앱은 이전과 정확히 동일하게 점수를 매깁니다. 아래 *AI 점수화* 참고.
- 읽기 전용 웹 데모는 현재 공개되어 있으며, 전체 `jobcron.app` 경험은 곧 출시됩니다.
  로컬 실행은 기여자와 셀프 호스팅 사용자를 위해 계속 제공합니다.

## 사용법 (Usage)

1. 첫 실행 시 프로필 입력 폼으로 이동합니다. 기술 스택, 지역, 그리고 제외
   키워드(절대 피하고 싶은 조건)를 입력한 뒤 저장하세요.
2. **스크랩 시작**을 누르고 스크랩이 실시간으로 흘러 들어오는 것을 지켜보세요.
3. 브리핑을 읽으세요 — 적합도순으로 정렬된 공고와 각 점수의 세부 내역이 표시됩니다.

하루에 한 번 실행하세요. 그것이 의식의 전부입니다.

## 알려진 한계 (Known limitations)

- **AI를 설정하면 매칭은 단순한 토큰 일치에만 의존하지 않습니다.** 1차 AI 레이어가
  공고 내용에서 경력·학력 요건을 해석한 뒤 점수화에 사용하며, AI를 사용할 수 없으면
  결정적 규칙 기반 경로로 전환합니다. 다만 사용자가 직접 입력한 제외 키워드는 문자 그대로
  확인하므로, "개발"은 "개발자"와 일치하지 않고 "야근"은 "야근 없음"에도 걸립니다.
- **브리핑은 오늘 올라온 공고입니다.** 첫 화면은 오늘 처음 발견된 것을 보여줍니다 — 그것이
  일일 의식입니다. 지금까지 스크랩한 모든 것은 전체 공고에 남아 있으므로(날짜순 또는
  적합도순으로 정렬 가능) 잃어버리는 것은 없습니다. 다만 매일 아침 당신에게 소리치지 않을
  뿐입니다.
- **신입 IT 전용입니다.** 출처들은 각자의 신입 / 엔트리(entry) 필터로 조회됩니다. 일반적인
  채용 검색이 아닙니다.
- 알림과 이력서 파싱은 없습니다. 프로덕션의 프로세스 내 스케줄러는 매일
  Asia/Seoul 05:00에 공고를 한 번 수집한 뒤 프로필이 있는 사용자를 순서대로 분석합니다.
- 첫 코호트의 가입은 접근 코드로 제한됩니다. 이메일 소유권은 확인하지 않으며 계정 복구는
  운영자 지원 방식입니다. 완전 공개 가입에는
  [보관된 후속 명세](docs/superpowers/archive/2026-07-22-multi-user-account-expansion/260715-multi-user-account-expansion.md#truly-public-signup-follow-up)의 보호 장치가 더 필요합니다.

## AI 점수화 (선택, v2.0, 당신의 키를 직접 사용)

기본적으로 꺼져 있습니다. 프로필 폼에서 **AI 분석 (선택)**을 열고, 제공자로
**Anthropic**, **OpenAI** 또는 **Gemini**를 선택하고, 당신의 API 키를 붙여넣은 뒤, 몇 개의 자유 서술형 목표(어떤 일을
좋아하는지, 무엇을 피하고 싶은지)를 입력하세요. 키는 사용자와 제공자별로 암호화되어
PostgreSQL에 저장되며, 저장한 뒤에는 다시 표시되지 않습니다.

AI를 켜면 1차 레이어가 각 공고의 경력 범위, 신입 지원 가능 여부, 학력 요건을 읽어 일반
점수에 반영합니다. 2차 레이어는 공고와 자유 서술형 목표를 비교하며, 공고에 붙은
**AI 분석** 칩(chip)을 클릭하면 각 점수 조정을 정당화하는 정확한 인용문을 볼 수 있습니다 —
인용문이 없으면 조정도 없습니다. 페이지별 **AI 평가** 버튼은 당신이 보고 있는 공고들을
목표 기준으로 다시 점수화하며, 직접 설정하는 일일 토큰 예산이 지출을 제한선 안에 묶어 둡니다.

이것은 **v2.0** 라인이며, 실제 AI 경로가 현실 세계에서 더 많은 검증을 거치는 동안
`-alpha` 사전 릴리스(prerelease)로 배포됩니다. AI 설정 여부와 무관하게 앱의 나머지 모든
기능은 동일하게 작동합니다.

## 고급 로컬 사용

대부분의 사용자는 정식 출시 후 `jobcron.app`을 이용하면 됩니다. 로컬 바이너리와
소스 빌드는 쓰기 가능한 앱을 직접 실행하려는 기여자와 셀프 호스팅 사용자를 위해
계속 제공합니다.

### 릴리스 바이너리 설치

[최신 릴리스](https://github.com/ohchanwu/jobcron/releases/latest)에서 사용하는
플랫폼용 바이너리를 내려받아 압축을 풀고 실행하세요. 앱은 <http://localhost:7777>에서
열립니다.

**macOS (Apple Silicon)**

```sh
curl -L https://github.com/ohchanwu/jobcron/releases/latest/download/jobcron_darwin_arm64.tar.gz | tar xz
./jobcron
```

**macOS (Intel)** — `jobcron_darwin_amd64.tar.gz`를 사용하세요.

**Linux (x86-64)**

```sh
curl -L https://github.com/ohchanwu/jobcron/releases/latest/download/jobcron_linux_amd64.tar.gz | tar xz
./jobcron
```

**Windows** — `jobcron_windows_amd64.zip`를 내려받아 압축을 풀고
`jobcron.exe`를 실행하세요.

### 첫 실행 시 참고 사항 (First-run notes)

- **macOS Gatekeeper**가 서명되지 않은 바이너리를 차단할 수 있습니다. 파일을 우클릭 →
  Open을 선택하거나, `xattr -d com.apple.quarantine ./jobcron`을 실행하세요.
- **Windows SmartScreen**: **More info → Run anyway**를 선택하세요.
- **기존 SQLite 앱에서 업그레이드:** 앱을 완전히 종료하세요. `jobs.db`, `-wal`
  보조 파일과 선택적 `ai_keys.json`을 보존하세요. 검증된 가져오기는
  [로컬 PostgreSQL 가이드][local-postgres-guide]를 따르세요.
  릴리스 압축 파일에는 패키징된 `jobcron-import`가 포함됩니다.
  소스 체크아웃은 필요 없습니다. 일반 시작 경로는
  기존 데이터베이스를 열거나 이동하지 않습니다.

[local-postgres-guide]: https://github.com/ohchanwu/jobcron/blob/main/deploy/local/README.md

플래그(flag): `--port` (기본값 `7777`), `--no-open` (브라우저를 열지 않음),
`--worknet-api-key` (워크넷 출처 활성화 — [data.go.kr](https://www.data.go.kr)에서
발급하는 무료 키이며 `JOBCRON_WORKNET_KEY` 환경 변수에서도 읽습니다), `--version`.

쓰기 가능한 앱 데이터는 PostgreSQL에 저장됩니다. `DATABASE_URL`이 없으면 일반 로컬
시작이 Docker Compose로 PostgreSQL 18을 관리하고 `jobcron-postgres18-cluster` 볼륨에
보존합니다. 기존 SQLite는 명시적인 `jobcron-import --sqlite` 마이그레이션 도구만
읽습니다.

### 로컬 대화형 미리보기 (Interactive localhost preview)

`scripts/preview-interactive.sh [포트]`를 실행하면(기본값 `17778`) 평소 앱 데이터를
건드리지 않고 `http://127.0.0.1:17778`에서 쓰기 가능한 일반 모드를 시험할 수
있습니다. 실행기는 사용자와 HTTP 포트별 원자적 잠금을 잡고, 관련 없는 리스너가 있으면
실행을 거부한 뒤 공유 `jobcron-local` PostgreSQL 서비스를 시작합니다. 그다음 고유한
일회용 데이터베이스, 비공개 상태 디렉터리, 암호화 키를 만듭니다. 미리보기는
비프로덕션 모드이며 스케줄러가 꺼져 있습니다.

종료할 때는 해당 미리보기 데이터베이스와 비공개 상태만 제거하며, 공유 서비스에
Compose `down`을 실행하지 않습니다. `JOBCRON_PREVIEW_KEEP=1`을 설정하면 둘 다
보존하고 정확한 수동 정리 명령을 출력합니다. 공유 Compose 프로젝트를 내리지 말고
출력된 명령을 사용하세요.

### 소스에서 빌드하기 (Build from source)

```sh
git clone https://github.com/ohchanwu/jobcron
cd jobcron
go build ./cmd/jobcron
```

Go 1.26 이상이 필요합니다. JavaScript 생명주기 테스트를 실행하려면 CI에 고정된
Node.js 22.15.1 LTS도 필요합니다. 배포되는 애플리케이션은 계속 순수 Go(Pure Go)이며,
CGO나 Node.js 런타임에 의존하지 않습니다.

### 로컬 PostgreSQL 개발

관리형 로컬 생명주기는 Docker 프로젝트 `jobcron-local`,
`postgres://postgres@127.0.0.1:55432/jobcron_dev?sslmode=disable`의 PostgreSQL 18,
이름이 고정된 볼륨 `jobcron-postgres18-cluster`를 사용합니다. Docker Engine,
`docker compose` 플러그인, 실행 중인 Docker 데몬이 필요합니다. URL 없는 일반 시작과
미리보기 모두 이 서비스를 자동으로 시작하거나 재사용합니다.

```sh
scripts/preview-interactive.sh
```

명시적 `DATABASE_URL`을 설정하면 관리형 Docker 시작을 건너뛰며, 대상 데이터베이스에는
기존 사용자가 정확히 한 명 있어야 합니다. 관리형 로컬 시작은 사용자 테이블이 비었을 때만
고정된 로그인 불가 소유자를 만들고, 검증된 가져오기 소유자 한 명은 변경 없이 재사용하며,
여러 사용자가 있는 모호한 상태는 거부합니다.

Compose 상태 검사나 컨테이너 메타데이터만으로는 충분하지 않습니다. 시작하려면
`127.0.0.1:55432`에 실제 TCP로 연결할 수 있어야 합니다. 실패하면 컨테이너와 볼륨을
보존하고 `ps` 및 `logs postgres` 진단 명령을 출력하며, 시작 경로는 상태를 자동으로
삭제하지 않습니다. 앱을 일반적으로 종료해도 PostgreSQL은 계속 실행됩니다. 명시적 중지,
초기화, 범위를 좁힌 잘못 생성된 컨테이너 복구 명령은
[deploy/local/README.md](deploy/local/README.md)에 있습니다.

## 기여하기 (Contributing)

이슈와 풀 리퀘스트(pull request)를 환영하며, 특히 다른 한국 개발자 구직자분들의 기여를
환영합니다. 보류 중인 향후 아이디어는 [feature-ideas.md](docs/product/feature-ideas.md)에 있습니다.

## 라이선스 (License)

MIT — [LICENSE](LICENSE) 참고.
