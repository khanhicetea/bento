<?php
require __DIR__ . '/db.php';
$m = db(); $m->query('INSERT INTO hits () VALUES ()');
$n = $m->query('SELECT COUNT(*) c FROM hits')->fetch_assoc()['c'];
echo json_encode(array('mark' => 'legacy-v1', 'php' => PHP_VERSION, 'page' => 'index', 'hits' => (int) $n)), "\n";
