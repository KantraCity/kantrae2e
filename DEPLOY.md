# Быстрый деплой: один VPS + один домен

Всё (Postgres, MinIO, 5 сервисов, Caddy с HTTPS от Let's Encrypt) поднимается одной командой
`docker compose` на одном сервере. Наружу открыт только Caddy (80/443). Приложения (Windows,
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
ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp && ufw --force enable

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
# Обновление
git pull && docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build
# Логи
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env logs -f --tail=100
# Бэкап Postgres + MinIO (зашифрованные данные пользователей) — например, раз в сутки по cron
deploy/backup.sh
```

Что видит сервер: логины и публичные профили, кто в каких чатах и когда пишет, размеры
сообщений. Чего не видит: содержимое сообщений, файлов и историй (MLS / XChaCha20 на
устройствах).
