#!/bin/sh
# Starts Flarum without root: the code stays in the image (/opt/flarum), and
# /data/forum holds config.php, extend.php, uploaded and compiled assets and
# the storage folder. The first start installs the forum and its admin.
set -eu
F=/opt/flarum
D=/data/forum
PHP="php -d memory_limit=256M -d upload_max_filesize=16M -d post_max_size=16M -d date.timezone=UTC"

mkdir -p "$D/public/assets/avatars" "$D/public/assets/extensions"
for s in cache formatter less locale logs sessions tmp views; do mkdir -p "$D/storage/$s"; done

# Glue that points Flarum at the image's code; rewritten on every start so an
# image update takes effect.
cat > "$D/site.php" <<'PHP'
<?php
require '/opt/flarum/vendor/autoload.php';

return Flarum\Foundation\Site::fromPaths([
    'base' => __DIR__,
    'public' => __DIR__.'/public',
    'storage' => __DIR__.'/storage',
    'vendor' => '/opt/flarum/vendor',
]);
PHP
cp "$F/flarum" "$D/flarum"
cp "$F/public/index.php" "$D/public/index.php"
[ -f "$D/extend.php" ] || cp "$F/extend.php" "$D/extend.php"
# Router for PHP's built-in server: existing files are served as they are,
# everything else goes to Flarum.
cat > "$D/router.php" <<'PHP'
<?php
$path = rawurldecode(parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/');
if ($path !== '/' && !str_contains($path, '..') && !str_ends_with($path, '.php') && is_file(__DIR__.'/public'.$path)) {
    return false;
}
$_SERVER['SCRIPT_NAME'] = '/index.php';
$_SERVER['SCRIPT_FILENAME'] = __DIR__.'/public/index.php';
chdir(__DIR__.'/public');
require __DIR__.'/public/index.php';
PHP

cd "$D"
echo "Waiting for the database at $DB_HOST..."
i=0
until $PHP -r 'new PDO("mysql:host=".getenv("DB_HOST").";dbname=".getenv("DB_NAME"), getenv("DB_USER"), getenv("DB_PASSWORD"));' 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -ge 90 ]; then echo "The database did not answer after 3 minutes." >&2; exit 1; fi
  sleep 2
done

if [ ! -f "$D/config.php" ]; then
  echo "Installing Flarum..."
  $PHP -r '
    echo json_encode([
      "debug" => false,
      "baseUrl" => getenv("FLARUM_URL"),
      "databaseConfiguration" => [
        "driver" => "mariadb", "host" => getenv("DB_HOST"), "port" => 3306,
        "database" => getenv("DB_NAME"), "username" => getenv("DB_USER"),
        "password" => getenv("DB_PASSWORD"), "prefix" => "",
      ],
      "adminUser" => [
        "username" => getenv("ADMIN_USERNAME"), "password" => getenv("ADMIN_PASSWORD"),
        "email" => getenv("ADMIN_EMAIL"),
      ],
      "settings" => ["forum_title" => getenv("FLARUM_TITLE"), "mail_driver" => "log"],
    ]);' > /tmp/flarum-install.json
  if ! $PHP flarum install --file=/tmp/flarum-install.json; then
    rm -f /tmp/flarum-install.json "$D/config.php"
    exit 1
  fi
  rm -f /tmp/flarum-install.json
else
  # Keep config.php's address in step with the app's address, then apply
  # migrations from a newer image.
  $PHP -r '
    $f = "/data/forum/config.php"; $c = include $f; $n = $c; $u = getenv("FLARUM_URL");
    if ($u) { $n["url"] = $u; }
    if (($n["database"]["driver"] ?? "") === "mysql") { $n["database"]["driver"] = "mariadb"; }
    if ($n !== $c) { file_put_contents($f, "<?php return ".var_export($n, true).";\n"); }'
  $PHP flarum migrate
  $PHP flarum cache:clear
fi

# Flarum's scheduler (cleanups, digests) runs every minute next to the server.
(while sleep 60; do $PHP flarum schedule:run >/dev/null 2>&1 || true; done) &

echo "Flarum is listening on port 8080"
$PHP -S 0.0.0.0:8080 -t "$D/public" "$D/router.php" &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null; wait "$SERVER"; exit 0' TERM INT
wait "$SERVER"
