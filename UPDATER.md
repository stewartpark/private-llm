# Auto-Updater Configuration Guide

## How It Works

The Private LLM macOS app now includes automatic update capability via Sparkle. When the app launches, it checks for a new version in the appcast.xml file. If an update is found:

1. The new DMG is downloaded silently to a secure temp location
2. The app waits until you close/quit Private LLM
3. On next launch, the updated bundle replaces the old one automatically
4. You're prompted to restart into the new version

## Manual Check

You can manually check for updates via the menu bar: right-click the icon → "Check for Updates..."

## Updating appcast.xml on Release

After releasing a new version (e.g., `v0.0.1-rc25`):

1. Get the DMG file size from GitHub release page or via CLI:
   ```bash
   gh release view v0.0.1-rc25 --json assets | jq '.assets[] | select(.name=="Private-LLM.dmg") | {name, size}'
   ```

2. Get the publish date (usually your commit date):
   ```bash
   git log -1 --format=%ci v0.0.1-rc25
   ```

3. Add a new `<item>` block to `appcast.xml` after the opening `<channel>` tag:
   ```xml
   <item>
       <title>Version 0.0.1-rc25</title>
       <link>https://github.com/stewartpark/private-llm/releases/tag/v0.0.1-rc25</link>
       <sparkle:version>0.0.1-rc25</sparkle:version>
       <sparkle:shortVersionString>0.0.1-rc25</sparkle:shortVersionString>
       <pubDate>Fri, 06 Mar 2026 12:34:56 +0000</pubDate>
       <enclosure url="https://github.com/stewartpark/private-llm/releases/download/v0.0.1-rc25/Private-LLM.dmg" length="YOUR_DMG_SIZE" type="application/octet-stream" />
       <sparkle:minimumSystemVersion>13.0</sparkle:minimumSystemVersion>
   </item>
   ```

4. Format the pubDate in RFC 822 format (e.g., "Fri, 06 Mar 2026 12:34:56 +0000")
5. Update `Info.plist` with new version numbers:
   - `<key>CFBundleShortVersionString</key>` → `<string>0.0.1-rc25</string>`
   - `<key>CFBundleVersion</key>` → `<string>25</string>` (increment build number)

6. Commit and push `appcast.xml` and `app/Resources/Info.plist` changes

## File Structure

```
Private LLM.app/
├── Contents/
│   ├── Frameworks/
│   │   └── Sparkle.framework/      # Update framework (1 MB)
│   ├── MacOS/
│   │   └── PrivateLLM              # Main executable (with updater code)
│   └── Resources/
│       ├── private-llm             # CLI binary
│       └── Sparkle.framework/UPdater.app  # Helper app for install relaunch
```

## Troubleshooting

### Update check fails
Make sure appcast.xml is publicly accessible:
```bash
curl https://raw.githubusercontent.com/stewartpark/private-llm/main/appcast.xml
```

### Users don't see update
1. Check `CFBundleShortVersionString` in Info.plist matches version in appcast.xml entry
2. Version comparison uses semantic versioning (e.g., 0.0.1-rc25 > 0.0.1)
3. Ensure DMG download URL is correct and publicly accessible

### Build errors about Sparkle framework not found
The build process downloads Sparkle automatically on each build. If this fails:
1. Ensure `.github/workflows/release.yml` step "Download Sparkle Framework" succeeds
2. On local builds, `make build` automatically downloads to `app/Resources/Sparkle.framework`

### DMG signature verification fails
If users get "cannot be verified" when installing the DMG update:
- The release workflow notarizes and stamps the DMG with Apple's timestamp
- This should succeed before upload; check GitHub Actions logs on any notarization failures
