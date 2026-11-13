---
title: What's new
lede: Which version of RivetPanel you run, and where every change is recorded.
icon: sparkle
aliases: [version]
related: [operate/upgrades, get-started/overview]
---

## Your version

The panel shows the version it runs, and this guide describes that version.
In the source, the root `VERSION` file is authoritative, and
`web/package.json` always carries the same number.

## Latest changes

<!-- figure: release-notes -->

The [changelog](../../../CHANGELOG.md) is the full record: every notable
release, and the changes not yet released under **Unreleased**. Read it
before upgrading; see [Upgrades](../07-operate/03-upgrades.md).

## What is still in progress

Staged work that is not finished is tracked in the
[implementation status](../../implementation-status.md), with the layer that
enforces each control: the application, the container runtime, the kernel or
network, or not yet enforced.

> [!NOTE]
> The panel still holds the Docker socket when it runs apps on its own host.
> Settings that are only stored, and the capabilities a remote agent reports,
> are not independent proof of network, file system or kernel enforcement.
> [not enforced]
