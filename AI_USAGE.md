# Süni intellektdən istifadə haqqında bəyannamə

**Layihə:** Urban Crisis Crowd Evacuation Engine — Fövqəladə hallar və şəhər təhlükəsizliyi şəbəkəsi
**Müsabiqə:** Digital Challenge Azerbaijan 2026 — Final mərhələsi, 15 noyabr 2026
**Komanda:** nihat1337, Fidan03, sevinj-jabrayilova

## 1. İstifadə olunan alət

Layihənin hazırlanmasında **Claude Code** (Anthropic, Claude modeli) adlı AI köməkçi proqramlaşdırma aləti istifadə olunmuşdur. Alət komanda üzvünün kompüterində terminal vasitəsilə, interaktiv rejimdə (“pair programming”) işlədilmişdir.

## 2. AI-nin rolu

AI aşağıdakı sahələrdə köməkçi vasitə kimi istifadə olunmuşdur:

| Sahə | AI-nin töhfəsi | Komandanın rolu |
|---|---|---|
| Arxitektura | Qatlı (layered) struktur, interfeyslər (Store, Router) və fallback mexanizmlərinin təklifi | Tələblərin təhlili, arxitektur qərarların (PostgreSQL + in-memory fallback, OSRM + simulyasiya) **seçimi və təsdiqi** |
| Kod | Go kodunun, HTML/JS interfeysinin, Dockerfile və compose faylının ilkin variantının yazılması | Hər modulun yoxlanılması, test edilməsi, lazım gəldikdə dəyişdirilməsi |
| Testlər | Unit və inteqrasiya testlərinin yazılması | Test ssenarilərinin (4 SOS / 5 dəq / 500 m, 95 % doluluq) **problem şərtindən** müəyyən edilməsi |
| Sənədləşmə | README, texniki izah sənədi və bu bəyannamənin tərtibi | Məzmunun doğruluğunun yoxlanılması |

## 3. AI-nin iştirak etmədiyi sahələr

- Problemin şərtinin oxunması, anlaşılması və tələblərə çevrilməsi — komanda tərəfindən.
- Məhsul qərarları: hansı xüsusiyyətlərin prioritet olduğu, münsiflər üçün demo ssenarisi — komanda tərəfindən.
- Münsiflər tərəfindən veriləcək ani texniki dəyişikliklərin təhlili və həll strategiyası — komanda tərəfindən (AI yalnız icra üçün köməkçi olacaq).
- Təqdimat və şifahi müdafiə — komanda tərəfindən.

## 4. Keyfiyyət nəzarəti

AI tərəfindən yaradılan hər bir kod parçası:
1. `go vet` və `go test ./... -race` ilə avtomatik yoxlanılmışdır;
2. Docker image-də qurularaq (`docker compose up --build`) real mühitdə işlədilmişdir;
3. Komanda üzvü tərəfindən oxunmuş və məntiqi başa düşülmüşdür — komanda kodun istənilən hissəsini izah etməyə hazırdır.

## 5. Məlumat və məxfilik

- Heç bir şəxsi məlumat, real vətəndaş məlumatı və ya məxfi açar AI alətinə ötürülməmişdir.
- Layihədəki bütün məlumatlar (rayonlar, sığınacaqlar, briqadalar) **simulyasiya məqsədli nümunə məlumatlardır**.
- Xarici xidmətlərdən yalnız açıq OSRM demo serveri (marşrutlaşdırma) və açıq xəritə laylarından (OpenStreetMap / CARTO) istifadə olunur.

## 6. Açıq mənbə komponentlər

| Komponent | Lisenziya | Təyinat |
|---|---|---|
| Go standart kitabxanası | BSD-3 | HTTP server, JSON, SSE |
| `github.com/jackc/pgx/v5` | MIT | PostgreSQL drayveri |
| Leaflet 1.9.4 | BSD-2 | Xəritə |
| OpenStreetMap / CARTO tiles | ODbL / CC-BY | Xəritə layları |
| OSRM demo server | BSD-2 | Küçə üzrə marşrut |

---

Komanda bu bəyannamənin doğruluğunu təsdiq edir. AI alətindən istifadə müsabiqənin qaydalarına uyğun, şəffaf və məsuliyyətli şəkildə həyata keçirilmişdir.

*15 noyabr 2026, Bakı*
