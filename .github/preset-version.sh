#!/bin/bash
# Stamps the release version into the apps (CI): 1.0.<run number>, the
# update key the apps trust (repository variable UPDATE_PUBLIC_KEY) and
# the repository the releases come from (desktop/update.go).
set -eu
V="1.0.${GITHUB_RUN_NUMBER}"
cat > desktop/version_ci.go <<GO
package main

func init() {
	appVersion = "$V"
	appBuild = ${GITHUB_RUN_NUMBER}
	updatePublicKey = "${UPDATE_PUBLIC_KEY:-}"
	updateRepository = "${GITHUB_REPOSITORY}"
}
GO
for p in desktop/build/ios/Info.plist desktop/build/ios/Info.dev.plist desktop/build/darwin/Info.plist desktop/build/darwin/Info.dev.plist; do
	sed -i.bak "s|<string>0.0.1</string>|<string>$V</string>|g" "$p" && rm -f "$p.bak"
done
sed -i.bak "s|version: \"0.0.1\"|version: \"$V\"|" desktop/build/config.yml && rm -f desktop/build/config.yml.bak
echo "version $V"
