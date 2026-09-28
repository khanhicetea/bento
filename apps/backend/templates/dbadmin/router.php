<?php
// Bento database browser front controller for the stock Adminer image.
//
// Every request arrives from the Bento backend's DB browser gateway, which
// authenticates the operator and injects the target binding's connection in
// X-Bento-* headers. This script refuses anything without the gateway token,
// forces the driver, server, user, and databases from those headers on every
// request (URL parameters cannot select another target), and never stores
// the password: the Adminer session only holds a placeholder.
declare(strict_types=1);

function bento_refuse(int $status, string $message): never
{
    http_response_code($status);
    header('Content-Type: text/plain; charset=utf-8');
    header('Cache-Control: no-store');
    echo $message, "\n";
    exit;
}

function bento_header(string $name): string
{
    $key = 'HTTP_' . strtoupper(str_replace('-', '_', $name));
    $value = $_SERVER[$key] ?? '';
    unset($_SERVER[$key]);
    return is_string($value) ? $value : '';
}

$bentoToken = @file_get_contents('/run/bento-dbadmin/gateway-token');
$bentoGiven = bento_header('X-Bento-Gateway-Token');
if (!is_string($bentoToken) || strlen(trim($bentoToken)) < 32 || !hash_equals(trim($bentoToken), $bentoGiven)) {
    bento_refuse(403, 'forbidden');
}

$bentoDriver = bento_header('X-Bento-Driver');
$bentoServer = bento_header('X-Bento-Server');
$bentoUser = bento_header('X-Bento-Username');
$bentoPassword = base64_decode(bento_header('X-Bento-Password'), true);
$bentoDatabases = array_values(array_filter(explode(',', bento_header('X-Bento-Databases')), 'strlen'));
$bentoHTTPS = bento_header('X-Bento-Https') === '1';
if (!in_array($bentoDriver, ['server', 'pgsql'], true)
    || !preg_match('~^[a-z][a-z0-9-]*$~', $bentoServer)
    || !preg_match('~^[A-Za-z0-9_]+$~', $bentoUser)
    || !is_string($bentoPassword) || $bentoPassword === '') {
    bento_refuse(400, 'invalid gateway request');
}

// Only Adminer's own routing parameters survive; the target is always ours.
foreach (['server', 'sqlite', 'pgsql', 'oracle', 'mssql', 'ext', 'username'] as $bentoKey) {
    unset($_GET[$bentoKey]);
}
$bentoRequestedDb = $_GET['db'] ?? null;
if ($bentoRequestedDb !== null && !in_array($bentoRequestedDb, $bentoDatabases, true)) {
    bento_refuse(403, 'this database is not part of the binding');
}
if ($bentoRequestedDb === null && $_SERVER['REQUEST_METHOD'] === 'GET' && $bentoDatabases && !$_GET) {
    // Land on the binding's first database (PostgreSQL users cannot connect
    // to databases outside the binding).
    header('Location: ?' . http_build_query([$bentoDriver => $bentoServer, 'username' => $bentoUser, 'db' => $bentoDatabases[0]]), true, 302);
    exit;
}
$_GET[$bentoDriver] = $bentoServer;
$_GET['username'] = $bentoUser;
if ($bentoHTTPS) {
    $_SERVER['HTTPS'] = 'on';
}

// Start the session before Adminer does so it cannot pick different cookie
// parameters, then satisfy Adminer's "has a password" check with a
// placeholder; credentials() below supplies the real password per request.
session_cache_limiter('');
session_name('adminer_sid');
session_set_cookie_params([
    'lifetime' => 0,
    'path' => strtr(preg_replace('~\?.*~', '', $_SERVER['REQUEST_URI']), [';' => '%3B', ',' => '%2C']),
    'secure' => $bentoHTTPS,
    'httponly' => true,
    'samesite' => 'Lax',
]);
session_start();
$_SESSION['pwds'][$bentoDriver][$bentoServer][$bentoUser] = 'bento-gateway';

function adminer_object()
{
    final class BentoGatewayPlugin extends \Adminer\Plugin
    {
        public function __construct(private string $password, private array $databases)
        {
        }

        public function name()
        {
            return "<a href='?' id='h1'>Bento DB browser</a>";
        }

        public function credentials()
        {
            return [\Adminer\SERVER, $_GET['username'], $this->password];
        }

        public function login($login, $password)
        {
            return true;
        }

        public function databases($flush = true)
        {
            return $this->databases;
        }

        public function permanentLogin($create = false)
        {
            return '';
        }

        public function head($dark = null)
        {
            // No egress from the data network: skip Adminer's version check.
            echo \Adminer\script('verifyVersion = () => { };');
        }
    }

    global $bentoPassword, $bentoDatabases;
    return new \Adminer\Plugins([new BentoGatewayPlugin($bentoPassword, $bentoDatabases)]);
}

chdir('/var/www/html');
require '/var/www/html/adminer.php';
