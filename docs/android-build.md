# Android release build

This build is the current httpSMS-style app (`com.httpsms`). It still uses Firebase Cloud Messaging, so a real `google-services.json` is required at build time. That file, the release keystore, and any private CA are supplied as GitHub Actions secrets. None of them belong in git.

The server address is not compiled into the APK. After install, set **Server URL** on the login screen or under **App Settings**.

## Secrets

Create these repository secrets (Settings → Secrets and variables → Actions). Encode files with `base64 -w 0` (GNU) or `base64 | tr -d '\n'`.

| Secret | Required | Purpose |
| --- | --- | --- |
| `GOOGLE_SERVICES_JSON_BASE64` | Yes | Base64 of the Firebase Android `google-services.json` for package `com.httpsms`. The workflow writes it to `android/app/google-services.json` and does not upload it. |
| `ANDROID_KEYSTORE_BASE64` | No | Base64 of the release keystore (`.jks` or `.keystore`). |
| `ANDROID_KEYSTORE_PASSWORD` | No | Keystore password. |
| `ANDROID_KEY_ALIAS` | No | Key alias inside the keystore. |
| `ANDROID_KEY_PASSWORD` | No | Key password. |
| `ANDROID_PRIVATE_CA_BASE64` | No | Base64 of a PEM CA certificate to bundle as a trust anchor. Leave unset to trust only the system store plus user-installed CAs. |

If any of the four keystore secrets is missing, the workflow still builds a release APK and signs it with the debug key. That APK installs for testing. It is not a store signing key. A pull request from a fork cannot read these secrets, so the workflow fails until they exist on this repository.

`android/app/google-services.json.example` shows the shape with placeholders. Copy a real file from the Firebase console (Android app id `com.httpsms`) when building locally, and keep the real file untracked.

## What the workflow does

`.github/workflows/android.yml` runs on pushes to `main`, on pull requests, and on manual dispatch.

1. Decodes `GOOGLE_SERVICES_JSON_BASE64` into `android/app/google-services.json`.
2. Decodes the keystore when all four signing secrets are present.
3. Decodes `ANDROID_PRIVATE_CA_BASE64` into a temp PEM and passes it as `ANDROID_PRIVATE_CA_FILE`.
4. Runs `./gradlew assembleRelease`.
5. Uploads `dist/httpsms-release.apk` as the `httpsms-release-apk` artifact.

Download the artifact from the workflow run. The job summary records whether the APK was release-signed or debug-signed.

## Local build

JDK 21 and an Android SDK that can compile API 37 are required.

```bash
cp android/app/google-services.json.example android/app/google-services.json
# replace that copy with the real Firebase file before a FCM build

cd android
export ANDROID_KEYSTORE_FILE="$HOME/release.keystore"   # optional
export ANDROID_KEYSTORE_PASSWORD="..."
export ANDROID_KEY_ALIAS="..."
export ANDROID_KEY_PASSWORD="..."
export ANDROID_PRIVATE_CA_FILE="$HOME/lan-ca.crt"       # optional PEM
./gradlew assembleRelease
```

The APK is written to `android/app/build/outputs/apk/release/`.

## Network security and a private CA

`android/app/network-security/network_security_config.xml` is copied into the APK at build time. It trusts:

- the system CA store
- user-installed CAs (`<certificates src="user" />`)

Android does not trust user CAs unless the app opts in. That opt-in is what makes a LAN server with a private or self-signed CA work after the CA is installed on the phone.

Cleartext HTTP stays disabled. The Server URL field accepts only `https://` origins.

To bake a CA into one build, set `ANDROID_PRIVATE_CA_BASE64` or `ANDROID_PRIVATE_CA_FILE` to a PEM (or DER) certificate. Gradle then uses `network_security_config.with_ca.xml` and a generated `res/raw/private_ca.crt`. Do not commit that certificate. User-installed CAs are enough for most phones and do not require a rebuild.

## Install the APK

1. Open the Actions run and download `httpsms-release-apk`.
2. Copy `httpsms-release.apk` to the phone, or:

```bash
adb install -r httpsms-release.apk
```

3. If Android blocks the install, allow installs from the source you used (the browser, Files, or `adb`). A debug-signed APK uses the Android debug certificate. Uninstall an older build signed by a different key before installing, or the install will fail with a signature mismatch.
4. Grant SMS, phone, and notification permissions when the app asks.

The application id stays `com.httpsms` so it matches the Firebase Android app that is already registered.

## Server URL

The login screen and **App Settings** both edit **Server URL**.

- Enter the HTTPS origin of the API only, for example `https://sms.example.com`. Do not include a path, query, or userinfo.
- The public httpSMS default (`https://api.httpsms.com`) is only a starting value. Replace it with the origin of the server you are running.
- Saving in **App Settings** updates the value used by later API calls. You do not need to log in again.
- The value is stored in app preferences on the phone. Changing it does not require a new APK.

A private hostname or IP works as long as it is an `https://` origin and the phone trusts the certificate (user CA or a CA bundled into that build).

## Install a CA on the phone

Use a PEM or CRT file of the CA that signed the server certificate (the CA, not the server leaf).

On Android 11 and newer the path is approximately:

1. Copy the CA file onto the phone.
2. Open **Settings → Security & privacy → More security & privacy → Encryption & credentials → Install a certificate → CA certificate**.
3. Confirm the warning. User CAs can be used to intercept traffic for apps that trust them, including this one.
4. Pick the CA file. Android may rename it under **Trusted credentials → User**.

Older releases use **Settings → Security → Encryption & credentials → Install a certificate → CA certificate**.

After the CA is in the user store, force-stop the app and open it again, then set **Server URL** to the LAN origin. No rebuild is required for a user-installed CA.

If the server certificate was signed by a CA you passed as `ANDROID_PRIVATE_CA_BASE64`, that CA is inside the APK and the phone does not need a separate install for this app. Other apps on the phone still need the user-store install if they should trust the same server.
