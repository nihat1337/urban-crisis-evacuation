# Süni intellektdən istifadə haqqında bəyannamə

**Layihə:** Urban Crisis Crowd Evacuation Engine — Fövqəladə hallar və şəhər təhlükəsizliyi şəbəkəsi
**Müsabiqə:** Digital Challenge Azerbaijan 2026 — Final mərhələsi, 15 noyabr 2026
**Komanda:** nihat1337, Fidan03, sevinj-jabrayilova

## 1. İstifadə olunan alət

Layihənin hazırlanmasında Anthropic şirkətinin **Claude** modelləri əsasında iki AI aləti istifadə olunmuşdur:

- **Claude Code** — AI köməkçi proqramlaşdırma aləti; komanda üzvünün kompüterində terminal vasitəsilə, interaktiv rejimdə (“pair programming”) işlədilmişdir (backend, testlər, sənədlər).
- **Claude tətbiqi** (claude.ai / Claude desktop) — istifadəçi interfeysinin dizaynı: dizayn maketlərinin hazırlanması, komandanın təsdiqindən sonra yalnız `web/` qovluğundakı dizayn fayllarına (HTML/CSS/JS) tətbiq edilməsi, brauzerdə yoxlanılması və sənədlərin yenilənməsi; həmçinin **SMS təxliyə modulunun** əvvəlki iş nüsxəsindən cari versiyaya köçürülməsi (endpointlərə giriş/rol tələbinin əlavə edilməsi, AZ/EN tərcümələri, bir kliklə göndərmə və «Uğurla göndərildi» pəncərəsi, avtomatik test).

Commit-lərdə AI iştirakı `Co-Authored-By: Claude …` sətri ilə qeyd olunub.

## 2. AI-nin rolu

AI aşağıdakı sahələrdə köməkçi vasitə kimi istifadə olunmuşdur:

| Sahə | AI-nin töhfəsi | Komandanın rolu |
|---|---|---|
| Arxitektura | Qatlı (layered) struktur, interfeyslər (Store, Router) və fallback mexanizmlərinin təklifi | Tələblərin təhlili, arxitektur qərarların (PostgreSQL + in-memory fallback, OSRM + simulyasiya) **seçimi və təsdiqi** |
| Kod | Go kodunun, HTML/JS interfeysinin, Dockerfile və compose faylının ilkin variantının yazılması | Hər modulun yoxlanılması, test edilməsi, lazım gəldikdə dəyişdirilməsi |
| Testlər | Unit və inteqrasiya testlərinin yazılması (o cümlədən SMS təxliyə modulu üçün) | Test ssenarilərinin (4 SOS / 5 dəq / 500 m, 95 % doluluq) **problem şərtindən** müəyyən edilməsi |
| UI dizayn | Dizayn maketləri (vətəndaş — açıq tema, dispeçer — qaranlıq tema), funksionallığı dəyişmədən HTML/CSS/JS-ə tətbiqi, açıq/qaranlıq tema dəstəyi, SVG ikonlar | Dizayn istiqamətinin seçimi, maketlərə rəy və **təsdiq**, “funksionallıq dəyişməməlidir” şərti, nəticənin yoxlanılması |
| Sənədləşmə | README, texniki izah sənədi və bu bəyannamənin tərtibi və yenilənməsi | Məzmunun doğruluğunun yoxlanılması |

## 3. AI-nin iştirak etmədiyi sahələr

- Problemin şərtinin oxunması, anlaşılması və tələblərə çevrilməsi — komanda tərəfindən.
- Məhsul qərarları: hansı xüsusiyyətlərin prioritet olduğu, münsiflər üçün demo ssenarisi — komanda tərəfindən.
- Münsiflər tərəfindən veriləcək ani texniki dəyişikliklərin təhlili və həll strategiyası — komanda tərəfindən (AI yalnız icra üçün köməkçi olacaq).
- Təqdimat və şifahi müdafiə — komanda tərəfindən.

## 4. Keyfiyyət nəzarəti

AI tərəfindən yaradılan hər bir kod parçası:
1. `go vet` və `go test ./... -race` ilə avtomatik yoxlanılmışdır;
2. Yeni modullar mövcud funksionallığı pozmamaq şərti ilə əlavə edilmiş, bütün köhnə testlər yenidən işlədilmişdir;
3. Docker image-də qurularaq (`docker compose up --build`) real mühitdə işlədilmişdir;
4. İnterfeys dəyişiklikləri brauzerdə (vətəndaş və dispeçer, açıq və qaranlıq tema, desktop və mobil ölçü) əsas ssenarilər üzrə sınanmışdır (SOS, status, prioritet rejim, profil, giriş, simulyasiya, briqada statusu, SMS, SMS təxliyə modulu — admin / operator / viewer rolları);
5. Komanda üzvü tərəfindən oxunmuş və məntiqi başa düşülmüşdür — komanda kodun istənilən hissəsini izah etməyə hazırdır.

## 5. Məlumat və məxfilik

- Heç bir şəxsi məlumat və ya real vətəndaş məlumatı AI alətinə ötürülməmişdir.
- **İstisna:** Google Maps Platform API açarı konfiqurasiya zamanı AI köməkçisi ilə yazışmada paylaşılmışdır. Açar repozitoriyaya yazılmır (`.env` faylı `.gitignore`-dadır); müsabiqədən sonra açarın məhdudlaşdırılması və ya yenilənməsi nəzərdə tutulur.
- Layihədəki bütün məlumatlar (rayonlar, sığınacaqlar, briqadalar, SMS modulunun demo vətəndaşları və `+99450…` nömrələri) **simulyasiya məqsədli nümunə məlumatlardır**; SMS şlüzü simulyasiya rejimindədir — real mesaj göndərilmir.
- Xarici xidmətlər: **Google Maps JavaScript API** (xəritə) və **Google Routes API** (piyada marşrut) — yalnız açar verildikdə; açıq **OSRM** demo serveri (marşrutlaşdırma) və **OpenStreetMap** xəritə layları. Xarici xidmətlər əlçatan olmadıqda sistem keşlənmiş marşrutlar və daxili yol qrafı ilə işləməyə davam edir.

## 6. İstifadə olunan komponentlər və xidmətlər

| Komponent | Lisenziya | Təyinat |
|---|---|---|
| Go standart kitabxanası | BSD-3 | HTTP server, JSON, SSE |
| `github.com/jackc/pgx/v5` | MIT | PostgreSQL drayveri |
| Leaflet 1.9.4 | BSD-2 | Xəritə |
| OpenStreetMap tiles | ODbL | Xəritə layları |
| OSRM demo server | BSD-2 | Küçə üzrə marşrut |
| Google Maps Platform (Maps JavaScript API, Routes API) | Google Maps Platform şərtləri (kommersiya xidməti) | Xəritə və piyada marşrut (açar varsa) |
| Leaflet.GridLayer.GoogleMutant 0.14.1 | Beerware | Google xəritəsinin Leaflet-də göstərilməsi |
| Inter, JetBrains Mono (Google Fonts) | SIL OFL 1.1 | İnterfeys şriftləri |

---

Komanda bu bəyannamənin doğruluğunu təsdiq edir. AI alətindən istifadə müsabiqənin qaydalarına uyğun, şəffaf və məsuliyyətli şəkildə həyata keçirilmişdir.

*15 noyabr 2026, Bakı*
