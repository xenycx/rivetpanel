---
title: Memory, Java and JVM arguments
lede: Understand how a Java server's heap follows its memory limit, which Java version it gets, and how to add JVM options such as Aikar's flags safely.
icon: sliders
glance:
  - Heap is 85% of the memory limit (80% for Velocity)
  - JVM options up to 2 KiB
  - Changes apply at the next start
related: [game-servers/overview, game-servers/administration]
---

## Memory

`SERVER_MEMORY`, the Java heap passed as `-Xmx`, is 85% of the server's
memory limit by default, and 80% for Velocity. The rest is left for the JVM
itself, threads and native memory, so a correctly sized heap does not push the
container past its limit [enforced: docker].

To give the server more heap, raise its memory limit. See
[Memory and CPU follow the node](01-overview.md#memory-and-cpu-follow-the-node).

## Java version

Unless you chose a Java image yourself, the panel picks the image with the
lowest Java version that runs the server. The requirement is the highest of
three sources:

| Source | Example |
|---|---|
| The provider's declared minimum (PaperMC's Fill API publishes one per version) | Java 25 for Velocity 4.x and Paper 26.x |
| The Minecraft release's own requirement (Mojang's version metadata, used through `java_from`) | Java 17 for 1.20.1, Java 25 for 26.x |
| The class-file version of the downloaded jar, read while it streams in | `META-INF/` entries of multi-release jars are ignored |

A Java image chosen earlier, by hand or by an older automatic pick, that is too
old for the downloaded version is replaced by a new enough one at
installation. It would only fail with `UnsupportedClassVersionError`. The
build output says so.

A server that already crashed with `UnsupportedClassVersionError` starts after
**Reinstall**.

### Choose a Java image by hand

Under **Configuration → Java and installation**, choose a Java image when a mod
needs a specific Java, or **Automatic** to let the panel pick.

Velocity is started with `--port {{SERVER_PORT}}`, so it listens on the
server type's fixed container port (25577) whatever `velocity.toml` says.

## JVM arguments

Java server types (every Minecraft type, including Velocity) have a
**JVM arguments** box on the **Configuration** tab. The options go to `java`
right after the heap size, through the panel-provided variable
`SERVER_JVM_ARGS`:

```
java -Xms128M -Xmx${SERVER_MEMORY}M ${SERVER_JVM_ARGS} … -jar server.jar nogui
```

### Presets

Presets fill the box with one click and can be edited afterwards.

| Preset | What it sets |
|---|---|
| Aikar's flags | The widely used G1 tuning for Minecraft servers. For a heap above 12 GB it uses the documented large-heap values: `G1NewSizePercent=40`, `G1MaxNewSizePercent=50`, `G1HeapRegionSize=16M`, `G1ReservePercent=15`, `InitiatingHeapOccupancyPercent=20` |
| ZGC (Java 21+, large heaps) | Generational ZGC. `-XX:+ZGenerational` is added on Java 21–23 only, because from Java 24 it is the only ZGC mode |
| None | No extra options |

Aikar's guidance also sets `-Xms` equal to `-Xmx`. The panel keeps `-Xms128M`
and manages the heap from the memory limit.

The page shows the server's Java version (from its Java image) and warns when
a preset needs a newer Java than the image provides. Choose a newer Java image
under **Java and installation** in that case.

### What is accepted

The panel validates the options when they are saved and again before every
start [enforced: panel].

- At most 2 KiB in total, and at most 512 characters per option.
- Options are separated by whitespace. Each must be one of:
  - `-XX:+Name`, `-XX:-Name`, `-XX:Name=value`
  - `-X…`, such as `-Xss1M` or `-Xlog:gc:file=gc.log`
  - `-Dname` or `-Dname=value`
  - `--add-opens=`, `--add-exports=`, `--add-reads=`, `--add-modules=`,
    `--enable-native-access=`
  - `--enable-preview`, `-ea`, `-da`, `-server`,
    `-verbose:gc|class|module|jni`
  - `-javaagent:<jar>[=options]` with a jar inside the server's files
- Only letters, digits and `. _ : + = , / @ % -` are allowed, so no quotes,
  `$`, backticks, `;`, `&`, `|`, redirects, globs or line breaks can reach the
  shell.

Refused, with an explanation:

| Refused | Why |
|---|---|
| `-Xmx`, `-Xms` and the `-XX` heap sizing options (`MaxHeapSize`, `MaxRAMPercentage`, …) | The heap follows the memory limit |
| `-jar`, `-cp`, `--module-path`, `-Xbootclasspath` | They change the jar, class path or module path |
| `-agentlib`, `-agentpath` | Native agents |
| `-XX:OnError`, `-XX:OnOutOfMemoryError` | They run commands |
| `@file`, `-XX:Flags`, `-XX:VMOptionsFile` | Option files |

> [!NOTE]
> RivetPanel does not check that the options suit the server's Java version.
> Options the JVM does not recognise stop the server at start, and the console
> shows the JVM's message.

### Applying a change

You can change the options while the server runs. They apply the next time it
starts. Until then a running server shows **Restart to apply**, and people
with the start/stop permission get a **Restart now** button.

Changing the options needs the same permission as the server's settings
(environment). Every change is recorded in the audit log as `game.jvm_args`
with the preset name or "custom options", never the options themselves.

### Servers created before JVM arguments

Existing servers keep the server type revision they were created with, which
has no JVM arguments box. On their **Configuration** tab, a person with full
control presses **Update to revision N** while the server is stopped to move
to the new revision.

Velocity used to have `-XX:+UseG1GC` in its command line. The new revision
moves it into the JVM arguments, and servers created from or updated to it
start with that value, so a ZGC preset does not conflict with it.

Custom and imported server types opt in with `startup.jvm_args: true`; see
[Blueprint format](08-administration.md#blueprint-format).
