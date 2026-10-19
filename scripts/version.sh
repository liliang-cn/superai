# Sourced by the package scripts. The app says the version of the tag being
# released (the About box, Info.plist, the .exe's properties), not whatever
# wails.json last said. VERSION overrides; wails.json is put back on exit.
VERSION="${VERSION:-$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)}"
VERSION="${VERSION#v}"
cp wails.json wails.json.orig
trap 'mv -f wails.json.orig wails.json' EXIT
perl -pi -e 's/("productVersion":\s*")[^"]*/${1}'"$VERSION"'/' wails.json
echo "==> version $VERSION"
