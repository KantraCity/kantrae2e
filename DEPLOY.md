# Быстрый деплой: один VPS + один домен

Всё (Postgres, MinIO, 5 сервисов, Caddy с HTTPS от Let's Encrypt, TURN-сервер coturn для звонков)
поднимается одной командой `docker compose` на одном сервере. Наружу открыты Caddy (80/443) и
coturn (3478 и UDP 49160–49200 — для звонков). Приложения (Windows,
macOS, Linux, Android, iOS) подключаются к `https://<ваш домен>`.

## 1. Что нужно

- VPS с Ubuntu 22.04/24.04 (или Debian 12): **2 vCPU, 2 ГБ RAM** (1 ГБ + swap тоже хватит),
  20 ГБ диска. Публичный IPv4.
- Домен или поддомен, например `chat.example.com`.

## 2. DNS

У регистратора/в Cloudflare создайте запись **A**: `chat.example.com → <IP сервера>`
(для Cloudflare — серое облако, «DNS only», иначе Let's Encrypt и WebSocket через прокси
придётся настраивать отдельно). Проверка: `ping chat.example.com` отвечает IP сервера.

## 3. Сервер (≈5–10 минут, в основном сборка)

```bash
ssh root@<IP>

# Docker
curl -fsSL https://get.docker.com | sh

# Файрвол (если включён ufw)
ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp \
  && ufw allow 3478 && ufw allow 49160:49200/udp && ufw --force enable   # 3478 и 49160-49200 — звонки
# У облачных провайдеров (Hetzner, Timeweb, Yandex Cloud…) те же порты откройте и в их файрволе/группе безопасности.

# Мало памяти? Добавьте swap на время сборки
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile

# Код
git clone https://github.com/KantraCity/kantrae2e.git && cd kantrae2e
git checkout claude/bold-sagan-h6l159        # пока ветка не влита в main

# Секреты (случайные) и домен
deploy/init-env.sh chat.example.com you@example.com

# Запуск
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build
```

Проверка (через минуту после старта, когда Caddy получит сертификат):

```bash
curl -s https://chat.example.com/kantra.auth.v1.AuthService/LookupUser \
  -H 'content-type: application/json' -d '{}'
# {"code":"unauthenticated",...}  <- сервер работает, HTTPS валидный
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env ps   # все healthy
```

`deploy/.env` — секреты сервера (подпись токенов, пароли БД и MinIO). Сохраните копию в надёжном
месте: без него не поднять этот же сервер с теми же данными (переписку он не защищает — она
и так зашифрована на устройствах).

### Если сборка падает

- **`proxy.golang.org … TLS handshake timeout`** (часто на российских VPS): Go-модули качаются
  через зеркало. Добавьте в `deploy/.env` строку
  `GOPROXY=https://goproxy.cn,https://goproxy.io,direct` и повторите команду запуска.
  Если не помогло — соберите образы по одному (меньше одновременных загрузок):
  ```bash
  C="docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env"
  for s in caddy minio s3init auth-service directory-service delivery-service history-service media-service; do $C build $s || break; done
  $C up -d
  ```
- **Сборка MinIO убита (`signal: killed`, exit 137)** — не хватило памяти: включите swap (выше).
- Повторный запуск продолжает с кэша: уже собранные слои не скачиваются заново.

## 4. Приложения с вашим доменом

Вариант А — **сразу в сборках**: в GitHub → Settings → Secrets and variables → Actions →
**Variables** добавьте `KANTRA_SERVER = https://chat.example.com` и перезапустите workflow
(Actions → CI → Re-run). Все сборки (Windows, macOS, Android, iOS) будут с вашим адресом.

Вариант Б — в любой готовой сборке на экране входа: «Сервер: … · **изменить**» →
`https://chat.example.com`.

Сборки — артефакты последнего запуска CI (Actions → CI → нужный запуск → Artifacts):

| Платформа | Артефакт | Установка |
|---|---|---|
| Windows | `kantra-windows-amd64` | распаковать, запустить `kantra-desktop.exe` |
| macOS (Apple Silicon) | `kantra-macos-arm64` | распаковать, перетащить `Kantra.app` в «Программы»; первый запуск — правый клик → «Открыть» (сборка не нотариализована); или `xattr -dr com.apple.quarantine /Applications/Kantra.app` |
| Android | `kantra-android-arm64` | `adb install -r kantra-desktop.apk` или скопировать APK на телефон и открыть (разрешить установку из этого источника) |
| iOS | `kantra-ios` | см. ниже |
| Linux | — | `make desktop` (нужны GTK4/WebKitGTK 6) |

### iOS

Apple не даёт ставить приложения без подписи. CI собирает **неподписанный** `Kantra.ipa`;
подпишите его своим Apple ID при установке:

- **Sideloadly** (Windows/macOS, бесплатно): подключите iPhone кабелем, перетащите
  `Kantra.ipa`, введите Apple ID → Start. На iPhone: Настройки → Основные → VPN и управление
  устройством → доверять разработчику. Для iOS 16+ включите «Режим разработчика»
  (Настройки → Конфиденциальность и безопасность).
- С **бесплатным** Apple ID подпись живёт 7 дней (потом переустановить тем же способом);
  с платным аккаунта разработчика ($99/год) — год, и можно раздавать через TestFlight.
- Или на Mac с Xcode: `make ios` / `cd desktop && wails3 task ios:xcode` и запуск из Xcode
  со своей командой подписи.

Ограничение iOS: система замораживает приложения в фоне, поэтому постоянное соединение
(как на Android) там невозможно — уведомления приходят, пока приложение открыто или только что
свёрнуто. Фоновые пуши на iOS возможны только через Apple Push (APNs), а это сервер Apple;
в проекте его сознательно нет.

## 5. Обслуживание

```bash
cd kantrae2e
# Обновление (init-env.sh --upgrade дописывает в deploy/.env секреты новых функций,
# например TURN_SECRET для звонков; уже настроенный .env не трогает)
git pull && deploy/init-env.sh --upgrade && docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build
# Логи
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env logs -f --tail=100
# Бэкап Postgres + MinIO (зашифрованные данные пользователей) — например, раз в сутки по cron
deploy/backup.sh
```

Что видит сервер: логины и публичные профили, кто в каких чатах и когда пишет, размеры
сообщений, факт и длительность звонков, если они идут через TURN. Чего не видит: содержимое
сообщений, файлов, историй и звонков (MLS / XChaCha20 / DTLS-SRTP на устройствах; ключи звонка
согласуются внутри MLS, поэтому сервер не может подменить их).

### Звонки

Аудио и видео идут напрямую между устройствами; если напрямую не получается (NAT, мобильный
интернет), их пересылает coturn на вашем сервере — уже зашифрованными. Групповые звонки — «каждый
с каждым», комфортно до 5–6 участников. Если звонки соединяются только в одной сети (Wi-Fi), а через
мобильный интернет — нет, проверьте, что порты 3478 и 49160–49200/udp открыты.
