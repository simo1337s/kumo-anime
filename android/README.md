# Kumo for Android

Made for TVs (a Fire TV Stick, Android TV), mainly to watch the libraries
the computers at home share; it works on phones and tablets too.

The app runs Kumo's server, the same as on a computer, built for Android
(`libkumo.so`, which Android extracts somewhere it may run it), and shows its
web UI full screen. On a TV the web UI is in TV mode (`web/src/lib/tv.ts`):
the remote's arrows move the focus, OK presses, Back closes what's open or
goes back, and the play/pause, rewind and fast-forward keys drive the
player.

- `app/src/main/java/app/kumo/MainActivity.java`: the screen, the remote's
  keys, and what the page asks of the app (`window.KumoAndroid`: keeping the
  screen on while a video plays, installing an update).
- `KumoServer.java`: starts the server (data in the app's files) and waits for
  it to answer.
- `Installer.java`: installs an update the server downloaded (the newest
  `android-v<version>` release's APK); Android asks first.

## Building

[`.github/workflows/android.yml`](../.github/workflows/android.yml) builds and
publishes it on every push. By hand, with the Android SDK, the NDK and
Gradle 8.11:

```bash
cd web && npm ci && npm run build && cd ..
rm -rf server/internal/webui/dist && cp -R web/dist server/internal/webui/dist
cd server
tc=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin
CGO_ENABLED=1 GOOS=android GOARCH=arm GOARM=7 CC=$tc/armv7a-linux-androideabi21-clang \
  go build -ldflags "-s -w" -o ../android/app/src/main/jniLibs/armeabi-v7a/libkumo.so ./cmd/kumo
CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC=$tc/aarch64-linux-android21-clang \
  go build -ldflags "-s -w" -o ../android/app/src/main/jniLibs/arm64-v8a/libkumo.so ./cmd/kumo
cd ../android && gradle assembleRelease
```

## Signing

Android installs an update over Kumo only when it's signed with the key of
the installed app, or a key that one passed on to (key rotation, Android 9
and newer). The first key, `packaging/android/kumo.jks` (password
`kumo-android`, alias `kumo`), is public, so the releases are signed with a
key of yours, in the repository's secrets, with the proof the first key
made that yours replaces it: the installed Kumo updates as before, and then
refuses an APK only the first key signed.

Make that key once, on your computer, with
[`packaging/android/new-signing-key.sh`](../packaging/android/new-signing-key.sh):
it makes it in `~/.local/share/kumo-android-key` (keep a backup: without it,
an installed Kumo can't update and must be reinstalled) and puts it in the
secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD` with the GitHub CLI (or says
what to paste where). The release workflow publishes nothing without them.
On Android 8 and older, which know no key rotation, the first key still
signs the updates.
