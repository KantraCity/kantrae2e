# kantrae2e — учебный E2EE-мессенджер на Go (MLS, self-hosted S3)

Реализация роадмапа: микросервисы на Go, крипто-ядро — [`mls-rs`](https://github.com/awslabs/mls-rs)
(RFC 9420) через cgo, зашифрованные бэкапы истории и медиа — в self-hosted MinIO.
Сервер никогда не видит открытый текст и не выполняет MLS-логику: он только
маршрутизирует байты и гарантирует порядок Commit'ов в группе.

```
cmd/kantra            CLI-клиент
desktop/              десктоп- и веб-клиент (Wails v3 + Svelte, стиль TG dark)
cmd/s3init            создание бакетов history/media (init-контейнер)
client/mls            cgo-мост к mls-rs (единственный "не Go" кусок)
client/store          SQLite клиента (modernc.org/sqlite, без cgo)
client/crypto         seed-фраза -> Argon2id -> history-key, XChaCha20-Poly1305
client/core           платформенно-независимое ядро клиента
client/bind           фасад только на string/int/[]byte/error для gomobile / Wails
mls-ffi/              Rust-крейт (staticlib) поверх mls-rs + C-заголовок
proto/, gen/          protobuf + сгенерированный connect-go код (buf)
pkg/                  общее: JWT, Postgres+миграции, bootstrap сервиса, blobstore (minio-go)
services/{auth,directory,delivery,history,media}
deploy/               docker-compose (dev/prod), Caddyfile(.dev), бэкап
e2e/                  сквозной тест всех DoD и бесшовного multi-device
```

## Быстрый старт

Нужны Go (версия из `go.mod`, тулчейн скачается сам), Rust и Docker.

```bash
make up          # Postgres, MinIO, 5 сервисов, Caddy на https://localhost
make dev-ca      # экспорт локального CA Caddy в deploy/caddy-root.crt
make cli         # собирает mls-ffi (cargo) и bin/kantra

export KANTRA_SERVER=https://localhost KANTRA_CA=deploy/caddy-root.crt
bin/kantra -db /tmp/alice.db register alice         # печатает seed-фразу
bin/kantra -db /tmp/bob.db   register bob
bin/kantra -db /tmp/alice.db create team
bin/kantra -db /tmp/alice.db invite team bob
bin/kantra -db /tmp/bob.db   chat team               # интерактивно, realtime через WebSocket
bin/kantra -db /tmp/alice.db send team "привет"
bin/kantra -db /tmp/alice.db sendfile team ./photo.jpg
bin/kantra -db /tmp/alice.db backup                  # зашифрованный бэкап истории в MinIO
```

Новое устройство: `kantra -db new.db login alice` (спросит пароль и seed-фразу). Больше ничего
делать не нужно: устройство восстанавливает бэкап, само вступает во все группы аккаунта и
получает от других участников сообщения, которых нет в бэкапе (см. «Несколько устройств»).
Потерянное устройство: `kantra devices`, затем `kantra revoke <device-id>`.

Проверка гейтвея из роадмапа (connect принимает JSON):

```bash
curl -k https://localhost/kantra.auth.v1.AuthService/Register \
  -H 'Content-Type: application/json' -d '{"username":"test","password":"test1234"}'
```

## Десктоп и веб

`desktop/` — Wails v3 (пресет `svelte`) поверх того же `client/core`. Одна кодовая база
собирается в двух вариантах:

```bash
make desktop       # окно (WebKitGTK / WebView2 / WKWebView)
make desktop-web   # Wails server mode: тот же UI в браузере на http://localhost:8090
make desktop-windows  # kantra-desktop.exe для Windows (кросс-сборка с Linux через MinGW)
```

Готовый `.exe` для Windows собирает CI на `windows-latest`: артефакт `kantra-windows-amd64`
в каждом прогоне (Actions → прогон → Artifacts).

Веб-вариант — это ваш MLS-клиент, запущенный локально: он хранит ключи этого устройства и
расшифрованные сообщения, поэтому по умолчанию слушает только `localhost`. Подробности — в
`desktop/README.md`.

## Тесты

```bash
make test-unit   # без внешних зависимостей
make test        # + Rust, сервисы на Postgres и e2e; TEST_DATABASE_URL по умолчанию
                 #   postgres://postgres:postgres@localhost:5432/postgres
# с настоящим MinIO вместо in-memory S3:
TEST_S3_ENDPOINT=127.0.0.1:9000 TEST_S3_ACCESS_KEY=... TEST_S3_SECRET_KEY=... make test
make lint
```

`e2e/e2e_test.go` поднимает все сервисы в одном процессе и проверяет DoD фаз 3–7 реальными
клиентами с отдельными SQLite: группа по сети, сервер видит только ciphertext, перезапуск
клиента, WebSocket, группа из 3+ и удаление участника, параллельные Commit'ы, медиа, бэкап и
восстановление истории на новом устройстве при выключенном старом.

## Как это устроено

**MLS-мост.** `mls-ffi` экспортирует C ABI (`include/mls_ffi.h`): `mls_create_group`,
`mls_create_commit` (add/remove), `mls_apply_pending_commit` / `mls_clear_pending_commit`,
`mls_join_group`, `mls_encrypt_application_message`, `mls_process_message` (обрабатывает и
Commit, и application — в mls-rs это один вызов), и т.д. Состояние (группы, прошлые эпохи,
секреты KeyPackage) держится в памяти Rust и каждое изменение пишется в change log, который Go
забирает (`TakeChanges`) и атомарно сохраняет в SQLite вместе с данными приложения. При старте
состояние загружается обратно — так клиент переживает перезапуск. Ошибки возвращаются кодом и
буфером (без thread-local: горутины мигрируют между потоками). Шифронабор
`CURVE25519_AES128`, провайдер `mls-rs-crypto-rustcrypto` (чистый Rust — проще кросс-компиляция
под мобильные платформы).

**Порядок Commit'ов** (delivery-service): `UPDATE groups SET current_epoch = current_epoch + 1
WHERE id = $1 AND current_epoch = $2`; 0 строк → `connect.CodeAborted` (HTTP 409). Клиент
создаёт Commit как *pending*, применяет его только после успеха; при конфликте сбрасывает,
догоняет пропущенные Commit'ы и пересобирает Commit на новой эпохе. Тест: 8 устройств × 5
раундов параллельных Commit'ов — в каждом раунде ровно один победитель.

**Выдача KeyPackage** атомарна: `UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED)
RETURNING payload`. Тест: 60 параллельных запросов за 20 KeyPackage'ами — каждый выдан ровно раз.
Клиент дополнительно проверяет, что identity внутри KeyPackage совпадает с запрошенным
устройством.

**Доставка.** Fan-out в `message_queue` (ссылка на `group_messages`, без копирования payload),
WebSocket `/v1/ws` шлёт `Envelope` (protobuf), клиент подтверждает `Ack`; неподтверждённое
передоставляется при реконнекте (at-least-once, клиент дедуплицирует по id). Есть и pull-режим
(`FetchPending`/`Ack`) — им пользуются CLI-команды.

**История.** history-key = Argon2id(seed-фраза BIP-39, соль = user_id), независим от MLS-эпох.
Чанк = пачка сообщений, XChaCha20-Poly1305, ключ объекта = BLAKE3(ciphertext); сервис
проверяет хеш, кладёт в бакет `history`, манифест — в Postgres.

**Медиа.** Файл шифруется случайным ключом, ciphertext → бакет `media`, ссылка
(hash + ключ) уходит внутри MLS application-сообщения.

## Несколько устройств (бесшовный вход)

В MLS каждое устройство — отдельный участник группы. Чтобы новое устройство сразу стало
полноценным, используется три механизма:

1. **Непрерывный бэкап.** Каждое устройство, знающее seed-фразу, выгружает новые сообщения в
   history-service через ~2 с после их появления (и досылает при закрытии клиента).
2. **Самостоятельное вступление (MLS External Commit).** Каждый Commit загружает на сервер
   GroupInfo новой эпохи. Новое устройство при входе вызывает `ListMyGroups` и для каждой группы
   аккаунта строит External Commit из GroupInfo, после чего `ExternalJoin`; порядок эпох
   проверяется так же, как для обычных Commit'ов. Онлайн никто быть не обязан.
   - Сервер разрешает это только пользователю, у которого уже есть устройство в группе, и не
     устройству, удалённому из неё (`group_bans`).
   - Каждый участник перепроверяет правило на своей стороне. Если через External Commit вошло
     устройство пользователя, которого в группе не было (например, скомпрометирован сервер),
     клиент выдаёт событие `security` и сам удаляет это устройство.
   - Запасной путь (нет актуального GroupInfo): `RequestJoin` рассылает участникам KeyPackage
     нового устройства (JOIN_REQUEST). Любой онлайн-участник, первыми — устройства того же
     пользователя, проверяет identity и добавляет его обычным Commit'ом.
3. **History sharing.** Сообщения, пришедшие, пока ни одно устройство пользователя не было
   онлайн, в бэкапе отсутствуют, и новое устройство не может их расшифровать (forward secrecy).
   Поэтому после вступления оно шлёт в группу `history_request` с временем последнего имеющегося
   сообщения, и онлайн-участник пересылает недостающее внутри текущей эпохи.
   - Отвечают только дополнительным устройствам уже состоявших в группе пользователей, и только
     начиная с момента, когда отвечающий впервые увидел этого пользователя в группе. Новый
     пользователь чужую прошлую переписку не получит.
   - Такие сообщения помечаются `origin = "shared"` (в CLI — `↺`): их подлинность держится на
     доверии к переславшему участнику, MLS-подписи исходного отправителя у них нет.

Отзыв устройства (`kantra revoke`) отзывает его в auth и удаляет из всех групп, где есть
текущее устройство. Сервер запоминает удаление, и отозванное устройство не может вернуться само.
Delivery и directory спрашивают у auth статус устройства через внутренний RPC
`AuthInternalService/DeviceStatus` (не проксируется гейтвеем, защищён `INTERNAL_TOKEN`, ответы
кэшируются на 30 с). Отозванное устройство получает отказ во всех RPC и WebSocket, даже с ещё
действующим JWT, а его KeyPackage'и больше не выдаются: повторный `invite` его не вернёт.

Ограничения: если на момент входа нового устройства никто из участников группы не онлайн,
пропущенное досылается, когда кто-то появится (запрос остаётся в очереди). Если пропущенные
сообщения не хранит ни один онлайн-участник, их не восстановить.

## Решения и отклонения от роадмапа

Согласовано заранее:
- **connect-go + protobuf** для всех RPC. Пути — `/kantra.<svc>.v1.<Service>/<Method>` вместо
  REST-таблиц роадмапа; соответствие: `POST /v1/register` → `AuthService/Register`,
  `GET /v1/keypackages/{user}/{device}` → `DirectoryService/FetchKeyPackage`,
  `POST /v1/groups/{id}/commit` → `DeliveryService/SendCommit` (409 → `Aborted`),
  `PUT/GET /v1/history/chunks/{hash}` → `HistoryService/PutChunk|GetChunk` и т.д.
  WebSocket остался на `/v1/ws`.
- **Caddy `handle` с полными путями** (без обрезки префикса) — сервисы обслуживают одни и те же
  пути и за гейтвеем, и напрямую.
- **`group_members`** в delivery-service; Commit несёт открытые метаданные маршрутизации
  (`added_device_ids` / `removed_device_ids`), не контент.
- **CLI-клиент** поверх переносимого ядра `client/core`; `client/bind` — готовый фасад для
  `gomobile bind` и Wails.

Решено по ходу (мелкие, стоит знать):
- **Go 1.26** вместо 1.23: актуальные pgx, minio-go, x/crypto и modernc/sqlite требуют ≥1.25/1.26.
- `key_packages.user_id` — нужен для `FetchKeyPackage(user, device)` и выдачи по всем устройствам
  пользователя без кросс-схемных join'ов.
- `group_members.joined_epoch`: application-сообщения прошлых эпох (допустим лаг в 2 эпохи) не
  рассылаются устройствам, вступившим позже — они всё равно не смогли бы их расшифровать.
- В auth добавлены `LookupUser` (username ↔ user_id) и `RefreshToken` (не выдаётся отозванным
  устройствам); токены бывают «аккаунтные» и «привязанные к устройству» (claim `did`).
- Бесшовный multi-device (по согласованию): External Commit + автоинвайт, автобэкап и history
  sharing; в delivery-service для этого добавлены `ListMyGroups`, `GetGroupInfo`, `ExternalJoin`,
  `RequestJoin`, колонки `group_info`, `group_members.user_id` и таблица `group_bans`.
- Бакеты создаёт init-контейнер `s3init` (minio-go) вместо ручного `mc mb`.
- **Образ MinIO:** по умолчанию `minio/minio:latest`, как в роадмапе, но переопределяется
  `MINIO_IMAGE`; `deploy/minio.Dockerfile` собирает MinIO из исходников, если upstream-образ
  недоступен (в этом окружении он не скачивался; MinIO сместил community-редакцию
  к распространению из исходников).

## Сверка ключей (safety numbers)

Каждый клиент запоминает ключ подписи каждого встреченного устройства (TOFU) и хранит его статус:
`?` не проверен, `✓` проверен, `!` ключ изменился.

```bash
kantra fingerprint          # отпечаток своего устройства: 30 цифр в 6 группах
kantra keys bob             # устройства Боба с отпечатками и статусом
kantra trust bob            # после сверки отпечатков вне мессенджера (голосом, лично)
kantra members team         # участники со статусами ✓ ? !
```

- **Ключ известного устройства изменился** (сервер подсунул чужой лист с тем же device id):
  событие `security`, отправка в группы, где есть этот ключ, блокируется (`ErrKeyConflict`).
  Выхода два: удалить пользователя из группы или, если смена ключа подтверждена вне мессенджера,
  выполнить `kantra trust <user> <device-id>`.
- **KeyPackage с другим ключом для известного устройства** отвергается при `invite` и при
  JOIN_REQUEST (`ErrKeyChanged`).
- **Новое устройство у проверенного контакта или у вашего аккаунта**: событие `new_device`,
  отпечаток стоит сверить.
- **Два участника с одной identity в одной группе** невозможны: mls-rs отвергает дубликаты сам.

## Деплой (фаза 8)

```bash
cp deploy/.env.example deploy/.env    # домен, email, секреты
make prod-up                          # docker compose -f deploy/docker-compose.prod.yml ...
```

Caddy собирается с плагином `caddy-ratelimit` (`deploy/caddy.Dockerfile`, Апгрейд 2): 5
запросов в минуту с IP на `Login`, `Register`, `RegisterDevice`; 60 в минуту на
`ExternalJoin`/`RequestJoin`; 1200 в минуту на остальные API. Сверх лимита — 429 с
`Retry-After`. За CDN/балансировщиком настройте `trusted_proxies`, иначе все клиенты будут
выглядеть одним IP.

Наружу публикуется только Caddy (80, 443/tcp, 443/udp для HTTP/3), TLS автоматический.
Postgres и MinIO (API и консоль) — только во внутренней сети; консоль — через
`ssh -L`. `caddy_data` — persistent volume (лимиты Let's Encrypt). `deploy/backup.sh` делает
операционный бэкап Postgres и тома MinIO — это не то же самое, что E2EE-бэкап пользователей.

## Чеклист «это не настоящий продакшен, пока...» (раздел 7)

- [x] MLS-логика не написана самостоятельно — только `mls-rs` через FFI
- [x] history-key и MLS epoch-секреты не пересекаются (Argon2id от seed-фразы)
- [x] Выдача KeyPackage атомарна и одноразова (тест на гонку)
- [x] Commit принимается только с проверкой `current_epoch` (тест на гонку)
- [x] Сервер не логирует plaintext и состояние групп (access-лог без тел; проверено на живом стеке)
- [x] Хранилище истории бэкапится отдельно от Postgres (`deploy/backup.sh`)
- [ ] Реального security-аудита нет

Известные ограничения учебной версии:
- **BasicCredential**: identity (`user_id:device_id`) не подписана доверенной стороной. Защита —
  сверка отпечатков (см. «Сверка ключей»): без неё злонамеренный сервер может выдать при первом
  знакомстве свой KeyPackage (TOFU не спасает от атаки на самый первый контакт).
- Отзыв устройства доходит до delivery/directory с задержкой до 30 с (кэш статуса). Если
  auth-service недоступен, используются закэшированные ответы, неизвестные устройства
  отклоняются.
- History sharing — пересланные сообщения подписаны не исходным отправителем, а переславшим.
- Локальная SQLite клиента не зашифрована (содержит расшифрованные сообщения и history-key).
- Realtime-уведомления внутри одного экземпляра delivery-service (плюс опрос раз в 15 с);
  для нескольких экземпляров — Postgres LISTEN/NOTIFY.
