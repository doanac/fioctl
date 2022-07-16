#!/bin/sh -e

tuf_bin=/tmp/tuf
curl -o ${tuf_bin} -L https://github.com/doanac/go-tuf/releases/download/andy-preview/tuf-linux-amd64
if [ "$(md5sum ${tuf_bin})"  != "df4cba3191332f9aa3f61f9b18c0fbe2  ${tuf_bin}" ] ; then
	echo "Invalid tuf binary"
	exit 1
fi
chmod +x ${tuf_bin}

git config user.name github-actions
git config user.email github-actions@github.com

if ! ${tuf_bin} status --expires "`date -d '+5 hour'`" snapshot ; then
	echo "refresing snapshot and timestamp metadata"
	${tuf_bin} snapshot
	${tuf_bin} timestamp
	${tuf_bin} commit
else
	if ! ${tuf_bin} status --expires "`date -d '+5 hour'`" timestamp; then
		echo "refresing timestamp metadata"
		${tuf_bin} timestamp
		${tuf_bin} commit
	fi
fi

git add repository/*
if [ -z "$(git status --porcelain)" ] ; then
	echo "metadata does not need refreshing"
else
	echo "committing changes to metadata"
	git commit -m "updated by github action"
	git push
fi
