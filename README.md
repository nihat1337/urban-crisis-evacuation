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
│  ├─ Google Routes (açar varsa)           ├─ postgres (pgx)     │
│  ├─ OSRM (xarici API)                    └─ memory (fallback)  │
│  ├─ RouteCache (son marşrutlar)  ← hər xarici API: 5 s timeout │
│  └─ Simulated (Dijkstra, daxili qraf)      + circuit breaker   │
└───────────────────────────────────────────────────────────────┘
```

### Modul 1 — Vətəndaş təhlükəsizlik modulu
- Rayon seçimi **və ya** koordinat (GPS / xəritəyə klik) üzrə ən yaxın toplanış məntəqələri (sığınacaq / park / stadion), cari **doluluq faizi** və **tövsiyə olunan marşrut** + 2 alternativ.
- **Bir kliklə SOS** — yanğın 🔥, dağıntı 🏚️, su basması 🌊, tibbi yardım 🚑.
- Təhlükəsizlik statusu: *Təhlükəsizəm / Təxliyə edirəm / Köməyə ehtiyacım var*.
- **Avtomatik alternativ marşrut**: yol bağlandıqda və ya məntəqə ≥ 90 % dolduqda server `route.invalidated` hadisəsi göndərir, vətəndaşın ekranı özü yenilənir.
- **♿ Prioritet SOS (hərəkət məhdudiyyəti / xüsusi qayğı)**: rol aktiv olduqda SOS avtomatik **Kritik prioritet #1** alır və yalnız **panduslu, maneəsiz** (əlil arabası / tibbi nəqliyyat üçün əlçatan) məntəqə və marşrutlar təklif olunur — bax §7.1.
- **Profil (parolsuz) + SMS bildirişi**: ad, **telefon** (məcburi), e-mail (istəyə bağlı). Qəsdən **hesab/parol yoxdur** — fövqəladə halda SOS həmişə bir klikdir, anonim SOS da qəbul olunur. Telefon varsa: dispeçer **zəng edə bilir** (📞), SOS gələn kimi sistem avtomatik **SMS** göndərir («ən yaxın məntəqə: …, 1.3 km, ~17 dəq, xəritə linki»), yol bağlananda/məntəqə dolanda **yeni SMS** gedir — mobil internet çöksə belə işləyir. SMS şlüzü `notify.Sender` interfeysidir: hazırda **simulyasiya** (log + dispeçer panelində jurnal), Twilio/operator şlüzü 1 faylla qoşulur.

### Modul 2 — Dispeçer modulu & analitik mühərrik
- Canlı panel: zonalar üzrə SOS sayı, məntəqələrin doluluğu, briqadaların statusu, yolların vəziyyəti — hamısı xəritədə.
- **Kritik Böhran alqoritmi** (`internal/crisis`):
  - eyni zonadan **və ya** 500 m radiusda, 5 dəqiqə ərzində **≥ 4 fərqli** vətəndaşdan SOS → 🔴 *«Əlavə xilasetmə briqadası göndər»*
  - toplanış məntəqəsinin doluluğu **> 95 %** → 🔴 *«Yeni toplanış nöqtəsi aç»*
- Qərarın **bir kliklə icrası**: ən yaxın boş briqada avtomatik təyin olunur / zonada müvəqqəti toplanış nöqtəsi yaradılır və dərhal vətəndaşların marşrutlarına daxil edilir.
- Simulyasiya düyməsi: seçilmiş zonaya 4 fərqli SOS göndərir — münsiflər alqoritmi canlı görə bilər.
- **SMS təxliyə modulu** (`internal/dispatch`, `internal/plan`, `internal/zones`): dispeçer siyahıdan **hadisə baş verən rayonu** seçir və **«Göndər»** düyməsini basır — sistem həmin rayonda telefonu olan vətəndaşları qruplara bölür (**hər məntəqəyə maksimum 100 nəfər**, real tutumun 90 %-i, məntəqə qalan insanların mərkəzinə ən yaxın seçilir; normal rejimdə 1,5 km, **fəlakət rejimində 3 km**) və hər kəsə **öz məntəqəsini, məsafəni və istiqaməti** SMS-lə göndərir: `TƏHLÜKƏ. Məntəqə: Park A, 420 m şimal-şərq. Xəritə: …`. Məntəqəsi olmayan və ya yeri bilinməyən vətəndaş xəritə linki ilə ümumi mesaj alır; ♿ prioritet vətəndaşlar öz (əlçatan marşrut) axınında qalır. Göndərişdən sonra **«Uğurla göndərildi»** pəncərəsi açılır — rayon, göndərilən SMS sayı və hər məntəqəyə düşən nəfər sayı. Göndərmədən əvvəl məntəqə yerləri yenidən yoxlanılır və rezerv olunur; eyni plan iki dəfə göndərilə bilməz (409).
  - **Aktiv rayonlar**: icra olunmamış həyəcanlar + son 15 dəqiqədə ≥ 3 fərqli vətəndaşdan SOS olan rayonlar *(hadisə baş verən)*, onlara 3 km-ə qədər olan rayonlar *(yaxın)*; «Bütün rayonlar» qalanlarını da göstərir.

### İstifadəçi interfeysi (dizayn)
Dizayn fövqəladə hal tətbiqləri üçün qəbul olunmuş prinsiplərə əsaslanır: **bir toxunuşla SOS**, yalnız vacib məlumat, qırmızı rəng yalnız təhlükə üçün, dispeçerdə ən kritik məlumat həmişə yuxarıda.
- **Vətəndaş** (standart: açıq tema): xəritə əsas sahədir; **4 SOS düyməsi həmişə görünür** (desktopda xəritənin altında, telefonda ekranın aşağısında sabit). Tövsiyə olunan məntəqə böyük kartda — **piyada dəqiqə**, məsafə, doluluq; alternativlər qısa sətirlərlə. Status — 3 düyməli keçid; ♿ prioritet rejimi — keçid düyməsi.
- **Dispeçer** (standart: qaranlıq tema): solda **qərar növbəsi** (qırmızı həyəcanlar böyük bir-klik düymələrlə, sonra SOS siyahısı — kritik ♿ siqnallar birinci); mərkəzdə xəritə və canlı göstəricilər (həyəcan, açıq SOS, kritik, boş briqada); sağda resurslar (**SMS təxliyə modulu** — rayon seçimi → «Göndər» → «Uğurla göndərildi» pəncərəsi; zonalar, briqadalar — statusu 1 kliklə dəyişən 3 düyməli keçid, məntəqələr, təlim rejimi, SMS jurnalı, sistem).
- **Tema**: başlıqdakı düymə ilə *avto / açıq / qaranlıq* seçilir (seçim brauzerdə yadda qalır); emoji əvəzinə vahid xətt ikonları (SVG); Inter + JetBrains Mono (rəqəmlər) şriftləri; mobil uyğunluq.

## 3. Docker ilə işə salma 🐳

### Variant A — tam sistem (tövsiyə olunur): tətbiq + PostgreSQL

```bash
git clone https://github.com/nihat1337/urban-crisis-evacuation.git
cd urban-crisis-evacuation
docker compose up --build
```

Açın: **http://localhost:8080** (vətəndaş) · **http://localhost:8080/dispatcher** (dispeçer — giriş: `admin / admin123`, həmçinin `operator / operator123`, `viewer / viewer123`)

> 8080 portu məşğuldursa: `PORT=8090 docker compose up --build` → http://localhost:8090

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
# {"status":"ok","routing_mode":"online","store":"postgres","avg_latency_ms":…,
#  "routing":{"mode":"auto","provider":"google","degraded":false,"cache_size":…,"osrm_available":true,...},...}
```

### Google Maps inteqrasiyası (istəyə bağlı)

Açar verildikdə əsas xəritə **Google Maps** (gecə stili + peyk seçimi), marşrutlar isə **Google Routes API (piyada)** olur. Açar yoxdursa, rədd edilirsə və ya Google xəritəsi 4 s ərzində yüklənmirsə (məs. gündəlik limit) sistem **avtomatik** OpenStreetMap-ə keçir (10 dəqiqəlik); marşrut isə Google Routes → OSRM → keş → lokal qraf ardıcıllığı ilə davam edir.

1. [Google Cloud Console](https://console.cloud.google.com/) → *APIs & Services → Library* → **Maps JavaScript API** və **Routes API**-ni aktivləşdirin (billing/hesab təsdiqi tələb olunur; Maps Platform-da ayda SKU başına 10K çağırış pulsuzdur). *Qeyd: köhnə «Directions API» legacy-dir — Routes API istifadə olunur.*
2. *Credentials → Create credentials → API key*:
   - **Brauzer açarı** — *Application restrictions: HTTP referrers* → `http://localhost:*/*`; *API restrictions:* Maps JavaScript API.
   - **Server açarı** (Routes üçün) — referrer məhdudiyyəti **olmadan** (və ya IP), *API restrictions:* Routes API.
3. Layihə kökündə `.env` faylı (gitignore-dadır):
   ```
   GOOGLE_MAPS_API_KEY=AIza...brauzer
   GOOGLE_DIRECTIONS_API_KEY=AIza...server
   ```
4. `docker compose up --build -d` — dispeçer panelində *Ümumi vəziyyət* kartı «Google Maps» və «Google Routes ✓» göstərməlidir.

   **Windows (PowerShell)** — `cp -n` işləmir, əvəzinə:
   ```powershell
   if (!(Test-Path .env)) { Copy-Item .env.example .env }
   notepad .env    # GOOGLE_MAPS_API_KEY və GOOGLE_DIRECTIONS_API_KEY sətirlərini doldurun
   ```

## 4. Demo ssenarisi (5 dəqiqə)

1. **Vətəndaş** (`/`): rayon seçin və ya xəritəyə klikləyin → mavi marşrut, böyük kartda tövsiyə olunan məntəqə (dəqiqə, məsafə, doluluq) + alternativlər.
2. **Dispeçer** (`/dispatcher`): `admin / admin123` ilə daxil olun → xəritədə yola klikləyin → yol **bağlanır** → vətəndaş ekranında marşrut **avtomatik dəyişir** (bildiriş: «Yol bağlandı»).
3. Dispeçerdə məntəqənin doluluq slayderini **96 %**-ə çəkin → qırmızı həyəcan «Yeni toplanış nöqtəsi aç» → **1 klik** → xəritədə müvəqqəti nöqtə yaranır, vətəndaşa yeni marşrut gəlir.
4. **Təlim / test rejimi → «Kütləvi SOS testi (4 siqnal)»** → qırmızı həyəcan «Əlavə xilasetmə briqadası göndər» → 1 klik → ən yaxın boş briqada *yoldadır* statusuna keçir və SOS-a təyin olunur. «Prioritet SOS» → kritik ♿ siqnal siyahının başına düşür.
5. **Xarici API dayanıqlılıq testi → «503 xətası»** → vətəndaşda «Oflayn / ehtiyat marşrut rejimi» banneri, marşrut keşdən / lokal qrafdan gəlir; «Normal» → onlayn rejimə qayıdır.
6. `operator / operator123` ilə daxil olun → briqada yönləndirmə və dayanıqlılıq testi kilidlidir (server 403 qaytarır) — RBAC.
7. **SMS təxliyə**: demo vətəndaşları əlavə edin — `curl -X POST localhost:8080/dev/seed -H 'X-Dispatcher-Key: dispatcher-secret' -d '{"district":"khatai","citizens":250}'` və `.../dev/incidents -d '{"district":"khatai","count":4}'` → dispeçerdə «SMS təxliyə modulu» → «Xətai» → **Göndər** → «Uğurla göndərildi» pəncərəsində 250 SMS və məntəqələr üzrə bölgü.
8. Dil keçidi (AZ/EN), tema düyməsi (avto / açıq / qaranlıq), `curl` ilə API, `docker compose logs` ilə strukturlaşdırılmış loglar.

## 5. API (v1)

Bütün cavablar JSON; xəta formatı `{"error":{"code":"...","message":"..."}}`, mesaj dili `?lang=` / `X-Lang` / `Accept-Language` ilə seçilir.

### Açıq (vətəndaş)

| Metod | Yol | Təsvir |
|---|---|---|
| GET | `/api/v1/health` | Sistem statusu (store, routing, versiya) |
| GET | `/api/v1/zones` | Rayonlar |
| GET | `/api/v1/shelters?lat&lng` \| `?zone=nasimi` | Ən yaxın məntəqələr, doluluq %, tövsiyə |
| GET | `/api/v1/evacuation/plan?lat&lng` \| `?zone=` [`&role=priority`] | Tövsiyə olunan marşrut + alternativlər; `role=priority` → yalnız ♿ əlçatan məntəqə/yollar |
| POST | `/api/v1/sos` | `{"citizen_id","role":"standard\|priority","type":"fire\|collapse\|flood\|medical","lat","lng","message"}` — `priority` → `priority: 1` |
| POST | `/api/v1/citizens/{id}/status` | `{"status":"safe\|evacuating\|need_help","role","lat","lng"}` |
| PUT | `/api/v1/citizens/{id}/profile` | `{"name","phone"(məcburi, +994-ə normallaşır),"email","role"}` — parolsuz profil |
| GET | `/api/v1/citizens/{id}` | Öz profilini oxumaq |
| GET | `/api/v1/roads` | Simulyasiya olunmuş yol/nəqliyyat endpointi |
| GET | `/api/v1/events` | **SSE** canlı hadisələr |
| GET | `/api/v1/i18n`, `/api/v1/i18n/{lang}` | Dillər və tərcümə lüğəti |
| GET | `/api/v1/config` | Brauzer üçün konfiqurasiya (Google Maps brauzer açarı varsa) |
| POST | `/api/v1/auth/login` | `{"username","password"}` → `{token (JWT), role, expires_at}` |
| GET | `/api/v1/metrics` | Gecikmə (60 s: avg/p50/p95/max, SOS ayrıca) + təhlükəsizlik sayğacları |

### Qorunan (dispeçer) — `Authorization: Bearer <JWT>` (rollar: viewer → oxu, operator → sahə əməliyyatları, admin → kritik); avtomatlaşdırma üçün `X-Dispatcher-Key: <açar>` (admin) də qəbul olunur

| Metod | Yol | Təsvir |
|---|---|---|
| GET | `/api/v1/dispatch/session` | Cari sessiya və rol (viewer) |
| GET | `/api/v1/dispatch/overview` | Panelin tam vəziyyəti (zonalar, SOS, məntəqələr, briqadalar, həyəcanlar, yollar) |
| GET | `/api/v1/dispatch/sos?since_minutes=30` | SOS siyahısı |
| POST | `/api/v1/dispatch/sos/{id}/resolve` | SOS-u bağla, briqadanı azad et |
| POST | `/api/v1/dispatch/sos/{id}/notify` | Vətəndaşa ən yaxın məntəqəni **SMS**-lə göndər (bir klik) |
| GET | `/api/v1/dispatch/notifications` | Göndərilən SMS-lərin jurnalı |
| PATCH | `/api/v1/dispatch/shelters/{id}` | `{"occupancy":580}` / `{"active":false}` → həyəcan yoxlanır |
| PATCH | `/api/v1/dispatch/roads/{id}` | `{"status":"blocked\|congested\|open"}` → vətəndaşlar yenidən marşrutlanır |
| PATCH | `/api/v1/dispatch/brigades/{id}` | `{"status":"available\|en_route\|busy","lat","lng"}` |
| POST | `/api/v1/dispatch/alerts/{id}/execute` | `{"action":"dispatch_brigade\|open_shelter"}` — bir kliklə qərar (admin) |
| POST | `/api/v1/dispatch/chaos` | `{"routing":"off\|503\|timeout"}` — xarici API kəsintisinin təlim simulyasiyası (admin) |
| GET | `/api/v1/dispatch/active-zones` | SMS modulu üçün aktiv rayonlar (`affected` / `nearby`, səbəb, məsafə) (viewer) |
| POST | `/api/v1/dispatch/zones/{zoneId}/sms-plan[?disaster=true]` | Rayon üçün SMS planı: alıcılar, məntəqələr və kvotalar (operator) |
| POST | `/api/v1/dispatch/sms-plans/{id}/confirm` | Planı göndər → `{sent, failed}`; ikinci dəfə 409, yer dəyişibsə 409 `quota_changed` (operator) |
| POST | `/dev/seed`, `/dev/incidents` | Demo: rayona N vətəndaş + 6 məntəqə / hadisə əlavə et (admin; `APP_ENV=production`-da söndürülür) |

> SMS modulu endpointləri `/api/dispatch/...` (spesifikasiya yolu) və `/api/v1/dispatch/...` ünvanlarında eynidir.

### SSE hadisələri
`sos.created` · `sos.priority` · `sos.updated` · `alert.created` · `alert.executed` · `shelter.updated` · `shelter.created` · `road.updated` · `brigade.updated` · `citizen.updated` · `notification.sent` · `routing.status` · `security.stats` · **`route.invalidated`** (`reason: road_blocked | shelter_full | shelter_opened | road_updated`)

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
| `ROUTING_MODE` | `auto` | `auto` \| `google` \| `osrm` \| `simulated` |
| `GOOGLE_MAPS_API_KEY` | *(boş)* | Google Maps JS (brauzer) açarı; boşdursa OpenStreetMap |
| `GOOGLE_DIRECTIONS_API_KEY` | `GOOGLE_MAPS_API_KEY` | Google Routes API (server) açarı; boşdursa Google marşrutu istifadə olunmur |
| `OSRM_URL` / `OSRM_PROFILE` | demo server / `driving` | Xarici marşrut API-si |
| `ROUTING_TIMEOUT` | `5s` | Hər xarici marşrut çağırışının limiti (Google + OSRM); aşılarsa xəta sayılır |
| `ROUTE_CACHE_TTL` | `24h` | Son keşlənmiş marşrutların saxlanma müddəti |
| `ROUTING_CHAOS` | *(boş)* | `503` \| `timeout` — xarici API kəsintisini başlanğıcdan simulyasiya et |
| `WALKING_SPEED_MPS` | `1.3` | Simulyasiyada təxliyə sürəti |
| `CRISIS_WINDOW` | `5m` | Böhran pəncərəsi |
| `CRISIS_RADIUS_M` | `500` | Böhran radiusu |
| `CRISIS_MIN_SIGNALS` | `4` | Minimum fərqli SOS |
| `SHELTER_ALERT_PCT` | `95` | > bu → qırmızı həyəcan |
| `SHELTER_WARN_PCT` | `90` | ≥ bu → məntəqə tövsiyə olunmur, marşrut dəyişir |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `20` / `40` | IP üzrə sorğu limiti (bütün API) |
| `SOS_RATE_PER_MIN` / `SOS_BURST` | `10` / `5` | IP üzrə `POST /sos` limiti |
| `SOS_DEDUPE_WINDOW` | `30s` | Dublikat SOS pəncərəsi |
| `SOS_FLOOD_THRESHOLD` | `60` | 10 s-də bu qədər SOS → flood rejimi |
| `TRUST_PROXY` | `false` | `X-Forwarded-For`-a etibar et (yalnız reverse proxy arxasında) |
| `JWT_SECRET` / `TOKEN_TTL` | demo / `8h` | Dispeçer JWT imzası və müddəti |
| `DISPATCH_USERS` | 3 demo hesab | `ad:parol:rol,…` — rollar `viewer` \| `operator` \| `admin` |
| `DEFAULT_LANG` | `az` | Standart dil |
| `PUBLIC_URL` | `http://localhost:8080` | SMS-dəki xəritə linkinin kökü |
| `NOTIFY_PROVIDER` | `simulated` | SMS şlüzü (`simulated`; Twilio/operator üçün `notify.Sender` implementasiyası) |
| `DEMO_MODE` | `0` | `1` → SMS təxliyə mesajlarının əvvəlinə `[DEMO/TEST]` əlavə olunur |
| `APP_ENV` | `development` | `production` → admin demo köməkçiləri (`/dev/seed`, `/dev/incidents`) söndürülür |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

## 7. Arxitekturanın elastikliyi — ani texniki dəyişikliklərə hazırlıq

Münsiflər tərəfindən final zamanı dəyişikliklər elan oluna bilər. Sistem bunun üçün qabaqcadan belə layihələndirilib:

| Mümkün dəyişiklik | Hazırlıq | Dəyişiklik üçün toxunulan yer |
|---|---|---|
| **Yeni DB** (MySQL, SQLite, Mongo, Redis…) | `store.Store` interfeysi; business-logic DB-dən xəbərsizdir | `internal/store/<yeni>/` — 1 fayl, `main.go`-da 1 sətir |
| **DB kəsintisi** | `STORE_FALLBACK` → in-memory ilə işləməyə davam edir; `/health` `degraded` göstərir | heç nə |
| **Xarici API kəsintisi** (Google / OSRM) | 5 s timeout + circuit breaker (3 xəta → 60 s) → son keşlənmiş marşrut (`source:"cache"`) → daxili yol qrafı (`source:"simulated"`); UI-da oflayn banneri — bax §7.1 №3 | heç nə — avtomatik |
| **Yeni marşrut provayderi** (Google, Mapbox, GraphHopper) | `routing.Router` interfeysi, `Chain` içində növbəyə qoşulur — **Google Routes API artıq qoşulub** (`internal/routing/google.go`, açar varsa birinci sırada) | `internal/routing/<yeni>.go` |
| **Xəritə provayderinin dəyişməsi** | Base layer `makeMap()`-da tək yerdədir: Google Maps (açar varsa) → avtomatik OpenStreetMap fallback (açar rədd edilərsə / 4 s-də yüklənməzsə); marker/marşrut kodu Leaflet-dir, dəyişmir | `web/static/common.js` |
| **Yeni dil** | `internal/i18n/locales/<kod>.json` — UI və API mesajları avtomatik | 1 JSON fayl |
| **Real SMS / push şlüzü** (Twilio, operator SMPP, FCM) | `notify.Sender` interfeysi; indi simulyasiya provayderi loglayır və jurnal yazır. SMS təxliyə modulu da eyni şlüzdən (`service.SendSMS` adapteri) istifadə edir, 20 paralel göndərişlə | `internal/notify/<yeni>.go` + `main.go`-da 1 sətir |
| **Kütləvi SMS qaydalarının dəyişməsi** (qrup ölçüsü, radius, klaster həddi) | Qruplaşdırma alqoritmi I/O-suz ayrıca paketdədir (`plan.Build`), sabitlər bir yerdədir | `internal/plan/plan.go`, `internal/zones/zones.go` |
| **Yeni SOS növü / status** | `domain` sabitləri + `Valid()` + locale açarı | 3 sətir |
| **Alqoritm hədlərinin dəyişməsi** (məs. 3 SOS / 3 dəq / 300 m / 90 %) | Hamısı env dəyişənidir | `docker compose up` ilə yenidən başlat, kod dəyişmir |
| **Yüksək yük** | IP üzrə token-bucket rate limit, body limit 1 MB, SSE non-blocking, Go goroutine-ları, stateless app → `docker compose up --scale app=3` | env |
| **Təhlükəsizlik tələbi** | **JWT + RBAC artıq tətbiq edilib** (viewer / operator / admin, `requireRole` middleware — bax §7.1 №4); SOS rate limit + dublikat filtri + flood rejimi; constant-time müqayisə, security headers, CORS, request-id, JSON audit log. OAuth/SSO üçün giriş nöqtəsi tək yerdədir | `internal/auth/`, `internal/api/middleware.go` |
| **Dizayn / tema dəyişikliyi** | Bütün rənglər CSS dəyişənlərindədir (açıq və qaranlıq tema), ikonlar bir SVG dəstindədir, səhifələr JS-dən asılı olmayan HTML strukturudur | `web/static/app.css`, `web/static/common.js` |
| **Yeni zona / məntəqə / briqada** | Seed data ayrı paketdə, API ilə də əlavə oluna bilər | `internal/seed/seed.go` |

### 7.1 Elan edilmiş dəyişikliklər və tətbiqi

Final zamanı münsiflər tərəfindən **4 dəyişiklik** elan olundu. Hamısı tətbiq edilib:

| № | Tələb | Nə dəyişdirildi | Necə yoxlanıldı |
|---|---|---|---|
| 1 | **«Hərəkət məhdudiyyətli / xüsusi qayğıya ehtiyacı olan vətəndaş» rolu (Prioritet SOS).** Bu rolun SOS-u avtomatik **Kritik prioritet (#1)** almalı; sistem ona yalnız **əlil arabası / tibbi nəqliyyat üçün əlçatan (panduslu, maneəsiz)** təxliyə marşrutu verməlidir. | • `domain`: `CitizenRole` (`standard`/`priority`), `SOS.Role`, `SOS.Priority` (1 = kritik, 2 = adi), `Shelter.Accessible`, `Road.Accessible`, `Route.Accessible`, `EvacuationPlan.Accessible`.<br>• `routing`: `Options{Accessible}` — Dijkstra qrafından pilləkənli/maneəli yollar çıxarılır; `Chain` əlçatan sorğularda OSRM-i **keçir** (ictimai OSRM pandus haqqında məlumat vermir).<br>• `service`: `role=priority` SOS → `priority=1` + `sos.priority` SSE hadisəsi; `Plan(accessible)` yalnız əlçatan məntəqələr + əlçatan yollar; briqada təyinatı kritik SOS-a üstünlük verir (store kritikləri əvvəl qaytarır).<br>• `store`: Postgres-də `ALTER TABLE … ADD COLUMN IF NOT EXISTS` ilə `accessible`, `role`, `priority` sütunları (mövcud DB pozulmur); `ListSOS` `priority ASC, created_at DESC`.<br>• `seed`: sığınacaqlar (pilləkən) `accessible=false`, park/stadion `true`; «Nizami küçəsi», «Mikayıl Müşfiq küçəsi» və sığınacaq girişləri `accessible=false`.<br>• API: `POST /sos {"role":"priority"}`, `GET /evacuation/plan?…&role=priority` (və ya `accessible=true`), `POST /citizens/{id}/status {"role"}`.<br>• UI: vətəndaşda ♿ «Hərəkət məhdudiyyəti / xüsusi qayğı» keçidi (cihazda yadda qalır), «♿ Əlçatan marşrut» nişanı, yalnız əlçatan məntəqələr; dispeçerdə kritik SOS **siyahının başında**, qırmızı ♿ marker + 120 m dairə, zonada ♿ sayğac, yollarda ♿/🚫♿ (nöqtəli xətt = pilləkən), «♿ Prioritet SOS» simulyasiya düyməsi. | `go test ./... -race`: `TestAccessibleRouteAvoidsNonAccessibleRoads` (pilləkənli diaqonal keçilmir), `TestPrioritySOSAndAccessiblePlan` (role=priority → priority 1; dispeçer siyahısında birinci; əlçatan planda heç bir sığınacaq yoxdur, bütün marşrutlar `accessible=true`; adi plan məhdudlaşdırılmır). Docker-də canlı: `curl ".../plan?lat=40.387&lng=49.83&role=priority"`. |
| 2 | **Bütün istifadəçi interfeysi AZ və EN dillərində dinamik dəyişdirilə bilməlidir (i18n).** | Arxitekturada əvvəlcədən mövcud idi: başlıqdakı **AZ / EN** seçicisi səhifəni yenidən yükləmədən dəyişir (`data-i18n` atributları + `/api/v1/i18n/{lang}`), seçim `localStorage`-da saxlanılır; API xəta mesajları `X-Lang` / `Accept-Language` üzrə tərcümə olunur. Yeni modul üçün 16 yeni açar hər iki lüğətə (`internal/i18n/locales/az.json`, `en.json`) əlavə edildi. Yeni dil = 1 JSON fayl. | UI-da AZ↔EN keçidi; `curl -H 'X-Lang: en' .../api/v1/sos -d '{"type":"x"}'` → ingiliscə xəta mesajı. |
| 3 | **Xarici xəritə/marşrut API-si sıradan çıxıb** (HTTP 503 və ya cavab > 5 s). Sistem çökməməli, circuit breaker/fallback avtomatik işə düşməli, məsafə/marşrut **lokal ehtiyat bazadan** (statik koordinat matrisi / son keşlənmiş marşrutlar) hesablanmalı və istifadəçiyə **«oflayn/ehtiyat marşrut rejimi»** bildirişi göstərilməlidir. | • **Timeout 5 s** hər xarici çağırışda (`ROUTING_TIMEOUT=5s`, Google Routes + OSRM); timeout = xəta kimi sayılır.<br>• **Circuit breaker** hər provayderdə: 3 ardıcıl xəta → 60 s açıq; açıq ikən xarici API heç çağırılmır (cavab ani).<br>• **Marşrut keşi** (`routing.RouteCache`, TTL 24 s, ~11 m dəqiqlik): hər uğurlu xarici marşrut saxlanılır; kəsintidə eyni başlanğıc→məntəqə üçün **son keşlənmiş marşrut** (`source:"cache"`) verilir.<br>• Keşdə yoxdursa → **lokal yol qrafı** (Postgres-dəki 21+ seqment, statik koordinat bazası) üzərində Dijkstra (`source:"simulated"`).<br>• Cavabda `route.offline=true`, `offline_reason`, `plan.mode="offline"`, `/health` → `routing_mode:"offline"` (status `ok` qalır — xidmət kəsilmir).<br>• **Vətəndaş UI**: narıncı banner (oflayn ikonu) «Oflayn / ehtiyat marşrut rejimi…», marşrut nişanı «Keş (son marşrut)» / «Ehtiyat (lokal qraf)»; xəritə layları da yüklənmirsə sxematik yol şəbəkəsi çəkilir. Bərpa olanda «Onlayn rejim» toast-u. Keçid SSE `routing.status` ilə **canlı** (3 s monitor).<br>• **Dispeçer UI**: «Xarici API dayanıqlılıq testi: Normal / 503 / Timeout» keçidi (`POST /dispatch/chaos`) — münsiflər kəsintini canlı görür; Sistem kartında «EHTİYAT REJİM», breaker, keş ölçüsü.<br>• Panic-recovery middleware + heç bir marşrut xətası 500 qaytarmır. | `TestOutage503ServesCacheThenLocalGraph` (503 → keş → lokal qraf → breaker açıq → bərpa), `TestOutageTimeoutFallsBackPromptly` (asılı qalan API → fallback < 2 s), `TestChaosSwitchSimulatesOutage`, `TestExternalRoutingOutageKeepsServing` (API: chaos 503 → plan 200 `mode=offline`, health `ok`, off → online). Docker-də canlı: 503 → `source:cache/simulated`; timeout → ilk sorğu ~5 s, breaker açılandan sonra ani. |
| 4 | **SOS Flood / spam hücumu** — eyni anda 500+ saxta SOS. SOS endpointləri **rate limiting + dublikat filtri** ilə qorunmalı (saxta sorğular dispeçer panelini bloklamamalı), kritik dispeçer əməliyyatları (briqada yönləndirmə) **JWT / RBAC** ilə təhlükəsizləşdirilməli, kütləvi sorğularda **orta gecikmə < 300 ms** qalmalıdır. | **Rate limiting (2 səviyyə):** bütün API üçün IP başına token-bucket (20 r/s) + `POST /sos` üçün ayrıca sərt limit (`SOS_RATE_PER_MIN=10`, burst 5) → 429 + `Retry-After`. `X-Forwarded-For` yalnız `TRUST_PROXY=true` olanda nəzərə alınır (IP saxtalaşdırma ilə limitdən qaçmaq olmur).<br>**Dublikat filtri:** eyni vətəndaş 30 s pəncərədə → `200 {duplicate:true}` (idempotent, yeni yazı yoxdur); flood zamanı eyni nöqtə+növ fərqli ID-dən → dublikat (botlar ID-ni dəyişir, koordinatı yox). O(1), yaddaş məhdud (20 000 açar).<br>**Flood rejimi:** 10 s-də > `SOS_FLOOD_THRESHOLD` (60) SOS → rejim açılır: **anonim** (profilsiz) siqnallar `suspect=true` ilə saxlanılır (audit), amma **canlı növbəyə düşmür, SSE ilə yayımlanmır, böhran alqoritminə sayılmır** (bot qırmızı həyəcan yarada bilməz); profilli vətəndaşlar normal xidmət alır. Panel 2 s-də bir aqreqat `security.stats` alır — sorğu başına hadisə yoxdur. Rejim trafik 1/4-ə düşəndən 30 s sonra bağlanır.<br>**JWT + RBAC:** `POST /auth/login` → HS256 JWT (stdlib HMAC, asılılıqsız), rollar `viewer < operator < admin`; `requireRole` middleware: viewer → oxu; operator → məntəqə/yol/SOS həll/SMS; **admin → briqada yönləndirmə (alert execute), briqada statusu, dayanıqlılıq testi**. Sabit-vaxt müqayisə, 8 s TTL, UI-da rol üstü düymələr kilidli (server onsuz da 403 qaytarır). Demo hesabları: `admin/admin123`, `operator/operator123`, `viewer/viewer123` (`DISPATCH_USERS`).<br>**Gecikmə:** `GET /api/v1/metrics` — 60 s pəncərədə avg/p50/p95/max, SOS üçün ayrıca; `/health`-də `avg_latency_ms`. Flood zamanı per-request log seyrəkləşir (hər 50-ci), SSE publish non-blocking, DB yazısı tək INSERT. Yük aləti: `go run ./cmd/loadtest -url … -n 600 -c 100 -spoof`. | Testlər: `TestSOSFloodProtection` (dublikat 200, flood → suspect, növbədə yoxdur, alert yoxdur, orta < 300 ms), `TestJWTRoleBasedAccess` (yanlış parol 401, viewer 403, operator yol dəyişir, operator briqada/chaos/alert execute 403, admin 200, saxta token 401), `auth` paketi testləri. **Canlı yük testi (Docker, 8090):** botnet 600 sorğu / 100 paralel → **orta 53 ms, p95 166 ms, 584 req/s, 201×600 (100 real + 500 suspect)**; tək IP 200 sorğu → **195× 429**, orta 24 ms; eyni nöqtə botu 200 → **199 dublikat**, orta 9 ms; flood zamanı dispeçer `overview` **33 ms**, 506 şübhəli siqnal növbədən gizli; operator → chaos **403**, admin **200**. |

## 8. Layihə strukturu

```
cmd/server/            giriş nöqtəsi, DI, graceful shutdown
cmd/loadtest/          yük testi aləti (SOS flood ssenarisi)
internal/
  domain/              modellər, haversine, ID
  config/              env → Config
  store/               Store interfeysi
    memory/            in-memory implementasiya
    postgres/          pgx implementasiya + idempotent migrasiya
  routing/             Router interfeysi: Google Routes, OSRM, breaker, RouteCache, Simulated (Dijkstra), Chain, chaos (təlim)
  auth/                JWT (HS256, stdlib) + rollar viewer / operator / admin
  notify/              SMS şlüzü interfeysi (notify.Sender) + simulyasiya provayderi
  dispatch/            SMS təxliyə modulu: aktiv rayonlar, plan, təsdiq və paralel göndəriş
  plan/                qruplaşdırma alqoritmi (≤ 100 nəfər / məntəqə, 90 % tutum) — I/O-suz, testli
  zones/               aktiv rayonların hesablanması (hadisə baş verən / yaxın)
  crisis/              Kritik Böhran alqoritmi
  events/              SSE hub
  service/             tətbiq məntiqi (plan, SOS, həyəcan icrası, overview)
  api/                 HTTP handlers, middleware, rate limit, testlər
  i18n/locales/        az.json, en.json
  seed/                Bakı nümunə məlumatları (8 rayon, 10 məntəqə, 5 briqada, 21 yol)
web/                   citizen.html, dispatcher.html (Leaflet), static/ (app.css — açıq/qaranlıq tema, common.js — ikonlar, tema, xəritə)
docs/                  Texniki izah sənədi (DOCX)
```

## 9. Test

```bash
go test ./... -race -count=1
```

Testlər: haversine · böhran alqoritmi (4 fərqli SOS, eyni vətəndaş spam, pəncərədən kənar, radius, 95 % həddi) · Dijkstra bağlı yolu və pilləkənli yolu keçir · Google → OSRM → keş → lokal qraf fallback, circuit breaker, 503 / timeout kəsintisi, chaos keçidi · JWT imza və rollar · API (plan, SOS → həyəcan → icra, yol bağlanması → detour, prioritet SOS, profil + SMS, xarici API kəsintisində xidmət, JWT/RBAC, SOS flood qoruması, SMS təxliyə modulu — giriş tələbi, plan, göndəriş, təkrar göndərişin qadağası) · SMS qruplaşdırması (250 nəfər, 6 məntəqə → 100 / 100 / 50).

## 10. Sənədlər

- [`docs/Texniki_Izah_Senedi.docx`](docs/Texniki_Izah_Senedi.docx) — texniki izah sənədi
- [`AI_USAGE.md`](AI_USAGE.md) — AI istifadəsi haqqında bəyannamə

---
*Bütün məlumatlar simulyasiya məqsədlidir. Xəritə: Google Maps (açar varsa) və ya © OpenStreetMap contributors. Marşrut: Google Routes API (piyada, açar varsa), OSRM demo server (məsafə/geometriya; müddət piyada sürəti ilə hesablanır) və daxili yol qrafı.*
