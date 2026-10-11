<?php
const MARK = 'php-v1';
header('Content-Type: application/json');
$out = ['mark' => MARK, 'php' => PHP_VERSION];
try {
    $pdo = new PDO(sprintf('mysql:host=%s;port=%s;dbname=%s', getenv('DB_HOST'), getenv('DB_PORT'), getenv('DB_DATABASE')),
        getenv('DB_USERNAME'), getenv('DB_PASSWORD'), [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
    $pdo->exec('CREATE TABLE IF NOT EXISTS hits (id INT AUTO_INCREMENT PRIMARY KEY, at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)');
    $pdo->exec('INSERT INTO hits () VALUES ()');
    $out['hits'] = (int) $pdo->query('SELECT COUNT(*) FROM hits')->fetchColumn();
    $out['mysql'] = $pdo->query('SELECT VERSION()')->fetchColumn();
} catch (Throwable $e) { http_response_code(500); $out['dbError'] = $e->getMessage(); }
try {
    $r = new Redis();
    $r->connect(getenv('REDIS_HOST'), (int) getenv('REDIS_PORT'));
    $r->auth([getenv('REDIS_USERNAME'), getenv('REDIS_PASSWORD')]);
    $out['redisCounter'] = $r->incr(getenv('REDIS_PREFIX') . 'counter');
} catch (Throwable $e) { http_response_code(500); $out['redisError'] = $e->getMessage(); }
$out['deployedAt'] = @file_get_contents(__DIR__ . '/../deployed-at.txt') ?: null;
echo json_encode($out), "\n";
