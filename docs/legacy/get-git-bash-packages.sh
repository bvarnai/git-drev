#!/bin/bash -e

echo "Checking 'zstd' command..."
if ! zstd --version; then
  echo "No 'zstd' command found"
  exit 1
fi
echo "OK"

echo "Getting 'libxxhash' package..."
# https://packages.msys2.org/package/libxxhash?repo=msys&variant=x86_64
curl -o libxxhash-0.8.1-1-x86_64.pkg.tar.zst -L http://repo.msys2.org/msys/x86_64/libxxhash-0.8.1-1-x86_64.pkg.tar.zst
tar -I zstd -xvf libxxhash-0.8.1-1-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'xxhash' package..."
curl -o xxhash-0.8.1-1-x86_64.pkg.tar.zst -L http://repo.msys2.org/msys/x86_64/xxhash-0.8.1-1-x86_64.pkg.tar.zst
tar -I zstd -xvf xxhash-0.8.1-1-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'libzstd' package..."
# https://packages.msys2.org/package/libzstd?repo=msys&variant=x86_64
curl -o libzstd-1.5.5-1-x86_64.pkg.tar.zst -L http://repo.msys2.org/msys/x86_64/libzstd-1.5.5-1-x86_64.pkg.tar.zst
tar -I zstd -xvf libzstd-1.5.5-1-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'liblz4' package..."
# https://packages.msys2.org/package/liblz4?repo=msys&variant=x86_64
curl -o liblz4-1.9.4-1-x86_64.pkg.tar.zst -L https://mirror.msys2.org/msys/x86_64/liblz4-1.9.4-1-x86_64.pkg.tar.zst
tar -I zstd -xvf liblz4-1.9.4-1-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'libopenssl' package..."
# https://packages.msys2.org/package/libopenssl?repo=msys&variant=x86_64
curl -o libopenssl-3.2.0-1-x86_64.pkg.tar.zst -L https://mirror.msys2.org/msys/x86_64/libopenssl-3.2.0-1-x86_64.pkg.tar.zst
tar -I zstd -xvf libopenssl-3.2.0-1-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'rsync' package..."
# https://packages.msys2.org/package/rsync?repo=msys&variant=x86_64
curl -o rsync-3.2.7-2-x86_64.pkg.tar.zst -L http://repo.msys2.org/msys/x86_64/rsync-3.2.7-2-x86_64.pkg.tar.zst
tar -I zstd -xvf rsync-3.2.7-2-x86_64.pkg.tar.zst
echo "OK"

echo "Getting 'util-linux' package..."
curl -o util-linux-2.35.2-1-x86_64.pkg.tar.zst -L http://repo.msys2.org/msys/x86_64/util-linux-2.35.2-1-x86_64.pkg.tar.zst
tar -I zstd -xvf util-linux-2.35.2-1-x86_64.pkg.tar.zst
echo "OK"
