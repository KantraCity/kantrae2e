#!/bin/sh
# Creates the keys for app releases (run once, on any Linux/macOS machine
# with openssl — e.g. your server):
#   - the Android signing key (kantra-release.p12): every APK must be
#     signed with the same key to install over the previous version;
#   - the update signing key (Ed25519): apps install only updates whose
#     manifest it signed.
# It prints what to put into GitHub: Settings → Secrets and variables →
# Actions. KEEP the directory it creates (a backup offline): with a lost
# Android key users have to reinstall the app; a leaked one lets others
# publish "updates".
set -eu
DIR="${1:-$HOME/kantra-release-keys}"
if [ -e "$DIR" ]; then
	echo "$DIR уже есть — существующие ключи не перезаписываю." >&2
	exit 1
fi
mkdir -p "$DIR" && chmod 700 "$DIR" && cd "$DIR"
umask 077

# Android: a PKCS#12 keystore (RSA 4096, 30 years).
PASS=$(openssl rand -hex 24)
openssl req -x509 -newkey rsa:4096 -sha256 -days 10950 -nodes \
	-subj "/CN=Kantra/O=Kantra" -keyout android-key.pem -out android-cert.pem 2>/dev/null
openssl pkcs12 -export -inkey android-key.pem -in android-cert.pem -name kantra \
	-out kantra-release.p12 -passout "pass:$PASS"
rm android-key.pem
printf '%s\n' "$PASS" > android-password.txt

# Updates: Ed25519.
openssl genpkey -algorithm ed25519 -out update-key.pem
SEED=$(openssl pkey -in update-key.pem -outform DER | tail -c 32 | base64)
PUB=$(openssl pkey -in update-key.pem -pubout -outform DER | tail -c 32 | base64)
printf '%s\n' "$PUB" > update-public-key.txt

b64() { base64 < "$1" | tr -d '\n'; }

cat <<MSG

Ключи созданы в $DIR — сохраните копию этой папки в надёжном месте (не в облаке).

GitHub → репозиторий → Settings → Secrets and variables → Actions:

  Вкладка Secrets → New repository secret:
    ANDROID_KEYSTORE_BASE64   = $(b64 kantra-release.p12)
    ANDROID_KEYSTORE_PASSWORD = $PASS
    UPDATE_SIGNING_KEY        = $SEED

  Вкладка Variables → New repository variable:
    UPDATE_PUBLIC_KEY         = $PUB

Следующий пуш соберёт подписанные приложения и опубликует релиз; начиная с
него приложения обновляются сами. Android: первый APK с этим ключом не
встанет поверх старой тестовой сборки — один раз удалите старое приложение.
MSG
