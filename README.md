# 🆘 Urban Crisis Crowd Evacuation Engine

**Fövqəladə hallar və şəhər təhlükəsizliyi şəbəkəsi — Vətəndaş SOS monitorinqi və dinamik təxliyə sistemi**

> Digital Challenge Azerbaijan 2026 · Final · 15 noyabr 2026 · ADA Universiteti
> Komanda: [@nihat1337](https://github.com/nihat1337) · [@Fidan03](https://github.com/Fidan03) · [@sevinj-jabrayilova](https://github.com/sevinj-jabrayilova)

[![CI](https://github.com/nihat1337/urban-crisis-evacuation/actions/workflows/ci.yml/badge.svg)](https://github.com/nihat1337/urban-crisis-evacuation/actions)

---

## 1. Problem

Zəlzələ, güclü sel, sənaye qəzaları və ya kütləvi tədbirlər zamanı yollar bağlanır, binalar zədələnir və panika nəticəsində çıxış yollarında tıxaclar yaranır. Vətəndaş ən yaxın təhlükəsiz toplanış məntəqəsinə nə qədər yol olduğunu və hansı yolun bağlı olduğunu görmür; dispeçer isə çoxsaylı SOS siqnalları içində ən kritik nöqtəni ayırd edə bilmir.

## 2. Həll — iki modul, bir mühərrik

```
┌──────────────────────┐        ┌──────────────────────────────┐
│  Modul 1 — Vətəndaş  │  SSE   │  Modul 2 — Dispeçer paneli    │
│  (/ )                │◀──────▶│  (/dispatcher)                │
│  • ən yaxın məntəqə  │        │  • canlı SOS / doluluq / briqada│
│  • doluluq %         │        │  • QIRMIZI HƏYƏCAN + 1-klik   │
│  • marşrut + alternativ│      │  • yol bağla / məntəqə aç     │
│  • 1-klik SOS, status│        │  • simulyasiya                │
└──────────┬───────────┘        └──────────────┬───────────────┘
           │            REST / JSON API v1      │
┌──────────▼────────────────────────────────────▼───────────────┐
│                     Go backend (net/http)                      │
│  service ─► crisis engine ─► events hub (SSE)                  │
│     │            │                                             │
│  routing.Chain   │     store.Store (interface)                 │
│  ├─ OSRM (xarici API, circuit breaker)   ├─ postgres (pgx)     │
│  └─ Simulated (Dijkstra, daxili yol qrafı)└─ memory (fallback) │
└───────────────────────────────────────────────────────────────┘
```

### Modul 1 — Vətəndaş təhlükəsizlik modulu
- Rayon seçimi **və ya** koordinat (GPS / xəritəyə klik) üzrə ən yaxın toplanış məntəqələri (sığınacaq / park / stadion), cari **doluluq faizi** və **tövsiyə olunan marşrut** + 2 alternativ.
- **Bir kliklə SOS** — yanğın 🔥, dağıntı 🏚️, su basması 🌊, tibbi yardım 🚑.
- Təhlükəsizlik statusu: *Təhlükəsizəm / Təxliyə edirəm / Köməyə ehtiyacım var*.
- **Avtomatik alternativ marşrut**: yol bağlandıqda və ya məntəqə ≥ 90 % dolduqda server `route.invalidated` hadisəsi göndərir, vətəndaşın ekranı özü yenilənir.

### Modul 2 — Dispeçer modulu & analitik mühərrik
- Canlı panel: zonalar üzrə SOS sayı, məntəqələrin doluluğu, briqadaların statusu, yolların vəziyyəti — hamısı xəritədə.
- **Kritik Böhran alqoritmi** (`internal/crisis`):
  - eyni zonadan **və ya** 500 m radiusda, 5 dəqiqə ərzində **≥ 4 fərqli** vətəndaşdan SOS → 🔴 *«Əlavə xilasetmə briqadası göndər»*
  - toplanış məntəqəsinin doluluğu **> 95 %** → 🔴 *«Yeni toplanış nöqtəsi aç»*
- Qərarın **bir kliklə icrası**: ən yaxın boş briqada avtomatik təyin olunur / zonada müvəqqəti toplanış nöqtəsi yaradılır və dərhal vətəndaşların marşrutlarına daxil edilir.
- Simulyasiya düyməsi: seçilmiş zonaya 4 fərqli SOS göndərir — münsiflər alqoritmi canlı görə bilər.

## 3. Docker ilə işə salma 🐳

### Variant A — tam sistem (tövsiyə olunur): tətbiq + PostgreSQL

```bash
git clone https://github.com/nihat1337/urban-crisis-evacuation.git
cd urban-crisis-evacuation
docker compose up --build
```

Açın: **http://localhost:8080** (vətəndaş) · **http://localhost:8080/dispatcher** (dispeçer, açar: `dispatcher-secret`)

```bash
docker compose logs -f app     # loglar (JSON)
docker compose down            # dayandır
docker compose down -v         # bazanı da sil
```

### Variant B — tək konteyner, bazasız (in-memory)

```bash
docker build -t urban-crisis-evacuation .
docker run --rm -p 8080:8080 urban-crisis-evacuation
```

### Variant C — Docker olmadan

```bash
go run ./cmd/server            # Go 1.25+; DATABASE_URL boş → in-memory
```

### Sağlamlıq yoxlaması

```bash
curl -s localhost:8080/api/v1/health | jq
# {"status":"ok","store":"postgres","routing":{"mode":"auto","osrm_available":true},...}
```

## 4. Demo ssenarisi (3 dəqiqə)

1. **Vətəndaş** (`/`): rayon seçin və ya xəritəyə klikləyin → mavi marşrut + alternativlər, doluluq faizləri.
2. **Dispeçer** (`/dispatcher`): xəritədə yola klikləyin → yol **bağlanır** → vətəndaş ekranında marşrut **avtomatik dəyişir** (toast: «Yol bağlandı»).
3. Dispeçerdə məntəqənin doluluq slayderini **96 %**-ə çəkin → 🔴 həyəcan «Yeni toplanış nöqtəsi aç» → **1 klik** → xəritədə ⛺ müvəqqəti nöqtə yaranır, vətəndaşa yeni marşrut gəlir.
4. **Simulyasiya → «4 SOS göndər»** → 🔴 «Əlavə xilasetmə briqadası göndər» → 1 klik → ən yaxın boş briqada 🚒 *yoldadır* statusuna keçir və SOS-a təyin olunur.
5. Dil keçidi (AZ/EN), `curl` ilə API, `docker compose logs` ilə strukturlaşdırılmış loglar.

## 5. API (v1)

Bütün cavablar JSON; xəta formatı `{"error":{"code":"...","message":"..."}}`, mesaj dili `?lang=` / `X-Lang` / `Accept-Language` ilə seçilir.

### Açıq (vətəndaş)

| Metod | Yol | Təsvir |
|---|---|---|
| GET | `/api/v1/health` | Sistem statusu (store, routing, versiya) |
| GET | `/api/v1/zones` | Rayonlar |
| GET | `/api/v1/shelters?lat&lng` \| `?zone=nasimi` | Ən yaxın məntəqələr, doluluq %, tövsiyə |
| GET | `/api/v1/evacuation/plan?lat&lng` \| `?zone=` | Tövsiyə olunan marşrut + alternativlər |
| POST | `/api/v1/sos` | `{"citizen_id","type":"fire\|collapse\|flood\|medical","lat","lng","message"}` |
| POST | `/api/v1/citizens/{id}/status` | `{"status":"safe\|evacuating\|need_help","lat","lng"}` |
| GET | `/api/v1/roads` | Simulyasiya olunmuş yol/nəqliyyat endpointi |
| GET | `/api/v1/events` | **SSE** canlı hadisələr |
| GET | `/api/v1/i18n`, `/api/v1/i18n/{lang}` | Dillər və tərcümə lüğəti |

### Qorunan (dispeçer) — başlıq `X-Dispatcher-Key: <açar>` və ya `Authorization: Bearer <açar>`

| Metod | Yol | Təsvir |
|---|---|---|
| GET | `/api/v1/dispatch/overview` | Panelin tam vəziyyəti (zonalar, SOS, məntəqələr, briqadalar, həyəcanlar, yollar) |
| GET | `/api/v1/dispatch/sos?since_minutes=30` | SOS siyahısı |
| POST | `/api/v1/dispatch/sos/{id}/resolve` | SOS-u bağla, briqadanı azad et |
| PATCH | `/api/v1/dispatch/shelters/{id}` | `{"occupancy":580}` / `{"active":false}` → həyəcan yoxlanır |
| PATCH | `/api/v1/dispatch/roads/{id}` | `{"status":"blocked\|congested\|open"}` → vətəndaşlar yenidən marşrutlanır |
| PATCH | `/api/v1/dispatch/brigades/{id}` | `{"status":"available\|en_route\|busy","lat","lng"}` |
| POST | `/api/v1/dispatch/alerts/{id}/execute` | `{"action":"dispatch_brigade\|open_shelter"}` — bir kliklə qərar |

### SSE hadisələri
`sos.created` · `sos.updated` · `alert.created` · `alert.executed` · `shelter.updated` · `shelter.created` · `road.updated` · `brigade.updated` · `citizen.updated` · **`route.invalidated`** (`reason: road_blocked | shelter_full | shelter_opened | road_updated`)

### Nümunə

```bash
# 4 fərqli vətəndaşdan SOS → 4-cüdə qırmızı həyəcan qayıdır
for i in 1 2 3 4; do
  curl -s -X POST localhost:8080/api/v1/sos -H 'Content-Type: application/json' \
    -d "{\"citizen_id\":\"c$i\",\"type\":\"fire\",\"lat\":40.387,\"lng\":49.830}" | jq -c '.alert.level'
done

# Həyəcanı icra et
curl -s -X POST localhost:8080/api/v1/dispatch/alerts/<ID>/execute \
  -H 'X-Dispatcher-Key: dispatcher-secret' -d '{"action":"dispatch_brigade"}' | jq
```

## 6. Konfiqurasiya (environment)

| Dəyişən | Standart | Mənası |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DATABASE_URL` | *(boş)* | Postgres DSN; boşdursa in-memory |
| `STORE_FALLBACK` | `true` | DB əlçatan deyilsə in-memory ilə davam et |
| `DISPATCHER_API_KEY` | `dispatcher-secret` | Dispeçer API açarı |
| `ROUTING_MODE` | `auto` | `auto` \| `osrm` \| `simulated` |
| `OSRM_URL` / `OSRM_PROFILE` / `OSRM_TIMEOUT` | demo server / `driving` / `4s` | Xarici marşrut API-si |
| `WALKING_SPEED_MPS` | `1.3` | Simulyasiyada təxliyə sürəti |
| `CRISIS_WINDOW` | `5m` | Böhran pəncərəsi |
| `CRISIS_RADIUS_M` | `500` | Böhran radiusu |
| `CRISIS_MIN_SIGNALS` | `4` | Minimum fərqli SOS |
| `SHELTER_ALERT_PCT` | `95` | > bu → qırmızı həyəcan |
| `SHELTER_WARN_PCT` | `90` | ≥ bu → məntəqə tövsiyə olunmur, marşrut dəyişir |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `20` / `40` | IP üzrə sorğu limiti |
| `DEFAULT_LANG` | `az` | Standart dil |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

## 7. Arxitekturanın elastikliyi — ani texniki dəyişikliklərə hazırlıq

Münsiflər tərəfindən final zamanı dəyişikliklər elan oluna bilər. Sistem bunun üçün qabaqcadan belə layihələndirilib:

| Mümkün dəyişiklik | Hazırlıq | Dəyişiklik üçün toxunulan yer |
|---|---|---|
| **Yeni DB** (MySQL, SQLite, Mongo, Redis…) | `store.Store` interfeysi; business-logic DB-dən xəbərsizdir | `internal/store/<yeni>/` — 1 fayl, `main.go`-da 1 sətir |
| **DB kəsintisi** | `STORE_FALLBACK` → in-memory ilə işləməyə davam edir; `/health` `degraded` göstərir | heç nə |
| **Xarici API (OSRM) kəsintisi** | Circuit breaker (3 xəta → 60 s) + Dijkstra ilə daxili yol qrafı; cavabda `source:"simulated"` | heç nə — avtomatik |
| **Yeni marşrut provayderi** (Google, Mapbox, GraphHopper) | `routing.Router` interfeysi, `Chain` içində növbəyə qoşulur | `internal/routing/<yeni>.go` |
| **Yeni dil** | `internal/i18n/locales/<kod>.json` — UI və API mesajları avtomatik | 1 JSON fayl |
| **Yeni SOS növü / status** | `domain` sabitləri + `Valid()` + locale açarı | 3 sətir |
| **Alqoritm hədlərinin dəyişməsi** (məs. 3 SOS / 3 dəq / 300 m / 90 %) | Hamısı env dəyişənidir | `docker compose up` ilə yenidən başlat, kod dəyişmir |
| **Yüksək yük** | IP üzrə token-bucket rate limit, body limit 1 MB, SSE non-blocking, Go goroutine-ları, stateless app → `docker compose up --scale app=3` | env |
| **Təhlükəsizlik tələbi** | Dispeçer API açarı (constant-time compare), security headers, CORS, request-id, JSON log; JWT/OAuth üçün `requireKey` middleware tək nöqtədir | `internal/api/middleware.go` |
| **Yeni zona / məntəqə / briqada** | Seed data ayrı paketdə, API ilə də əlavə oluna bilər | `internal/seed/seed.go` |

### 7.1 Elan edilmiş dəyişikliklər və tətbiqi

> ⏳ **Hələ elan edilməyib.** Münsiflər dəyişikliyi elan edən kimi bu bölmə doldurulacaq: *nə tələb olundu → nə dəyişdirildi (fayl/commit) → necə yoxlanıldı.*

## 8. Layihə strukturu

```
cmd/server/            giriş nöqtəsi, DI, graceful shutdown
internal/
  domain/              modellər, haversine, ID
  config/              env → Config
  store/               Store interfeysi
    memory/            in-memory implementasiya
    postgres/          pgx implementasiya + idempotent migrasiya
  routing/             Router interfeysi: OSRM (breaker), Simulated (Dijkstra), Chain
  crisis/              Kritik Böhran alqoritmi
  events/              SSE hub
  service/             tətbiq məntiqi (plan, SOS, həyəcan icrası, overview)
  api/                 HTTP handlers, middleware, rate limit, testlər
  i18n/locales/        az.json, en.json
  seed/                Bakı nümunə məlumatları (8 rayon, 10 məntəqə, 5 briqada, 21 yol)
web/                   citizen.html, dispatcher.html (Leaflet), static/
docs/                  Texniki izah sənədi (DOCX)
```

## 9. Test

```bash
go test ./... -race -count=1
```

Testlər: haversine · böhran alqoritmi (4 fərqli SOS, eyni vətəndaş spam, pəncərədən kənar, radius, 95 % həddi, dublikat) · Dijkstra bağlı yolu keçir · OSRM fallback & circuit breaker · API (auth, plan, SOS → həyəcan → icra, yol bağlanması → detour).

## 10. Sənədlər

- [`docs/Texniki_Izah_Senedi.docx`](docs/Texniki_Izah_Senedi.docx) — texniki izah sənədi
- [`AI_USAGE.md`](AI_USAGE.md) — AI istifadəsi haqqında bəyannamə

---
*Bütün məlumatlar simulyasiya məqsədlidir. Xəritə: © OpenStreetMap, © CARTO. Marşrut: OSRM demo server.*
