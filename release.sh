#!/bin/bash

set -e

cd "$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

command -v github-release >/dev/null 2>&1 || { echo >&2 "Please install github-release."; exit 1; }

if [[ -z "$GITHUB_TOKEN" ]]; then
    if [[ "$SKIP_UPLOAD" != "true" ]]; then
        echo "GitHub token not set"
        exit 1
    fi
fi

rm -rf build
mkdir -p build

if [[ -z "$(git describe --abbrev=0 --tags 2>/dev/null)" ]]; then
    echo "No tags found"
    export NO_TAGS=true
    export APP_VERSION=v0.0.1
else
    export NO_TAGS=false
    export APP_VERSION="$(git describe --tags --always --dirty)"
fi

echo "APP_VERSION: $APP_VERSION"

echo "## Changelog" | tee -a build/release-notes.md
if [[ -f "./docs/notes/$APP_VERSION.md" ]]; then
    cat "./docs/notes/$APP_VERSION.md" | tee -a build/release-notes.md
fi
if [[ "$NO_TAGS" == "true" ]]; then
    echo "$(git log --oneline)" | tee -a build/release-notes.md
else
    echo "$(git log $(git describe --tags --abbrev=0 HEAD^)..HEAD --oneline)" | tee -a build/release-notes.md
fi

echo "Installing toolchains"
wget "https://github.com/bblfsh/client-scala/releases/download/v1.5.2/osxcross_3034f7149716d815bc473d0a7b35d17e4cf175aa.tar.gz" -O- | tar -xzf -
export "PATH=$PWD/osxcross/bin:$PATH"
sudo dpkg --add-architecture i386
sudo apt update || echo "Warning: apt update failed"
sudo apt install -y aptitude
sudo aptitude install -y gcc gcc-multilib gcc-mingw-w64-i686 zlib1g-dev:i386 gcc-arm-linux-gnueabihf clang libc6-dev-i386
wget http://mirrors.kernel.org/ubuntu/pool/universe/libz/libz-mingw-w64/libz-mingw-w64_1.2.8+dfsg-2_all.deb
wget http://mirrors.kernel.org/ubuntu/pool/universe/libz/libz-mingw-w64/libz-mingw-w64-dev_1.2.8+dfsg-2_all.deb
sudo dpkg -i *.deb
rm -rfv *.deb

make cross
rm -rf osxcross

if [[ "$SKIP_UPLOAD" != "true" ]]; then
    echo "Creating release"
    echo "Deleting old release if it exists"
    GITHUB_TOKEN=$GITHUB_TOKEN github-release delete \
        --user geek1011 \
        --repo kobopatch \
        --tag $APP_VERSION >/dev/null 2>/dev/null || true
    echo "Creating new release"
    GITHUB_TOKEN=$GITHUB_TOKEN github-release release \
        --user geek1011 \
        --repo kobopatch \
        --tag $APP_VERSION \
        --name "kobopatch $APP_VERSION" \
        --description "$(cat build/release-notes.md)"

    for f in build/kobop*;do 
        fn="$(basename $f)"
        echo "Uploading $fn"
        GITHUB_TOKEN=$GITHUB_TOKEN github-release upload \
            --user geek1011 \
            --repo kobopatch \
            --tag $APP_VERSION \
            --name "$fn" \
            --file "$f" \
            --replace
    done

    for f in build/cssextract*;do 
        fn="$(basename $f)"
        echo "Uploading $fn"
        GITHUB_TOKEN=$GITHUB_TOKEN github-release upload \
            --user geek1011 \
            --repo kobopatch \
            --tag $APP_VERSION \
            --name "$fn" \
            --file "$f" \
            --replace
    done
fi