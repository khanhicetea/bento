<?php
// php q.php "SQL" — run one SQL statement against the app's first MySQL binding, print rows as TSV
$p = new PDO(sprintf('mysql:host=%s;port=%s;dbname=%s', getenv('DB_HOST'), getenv('DB_PORT'), getenv('DB_DATABASE')), getenv('DB_USERNAME'), getenv('DB_PASSWORD'), [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
try { $s = $p->query($argv[1]); foreach ($s->fetchAll(PDO::FETCH_NUM) as $r) echo implode("\t", $r), "\n"; } catch (Throwable $e) { echo 'ERR ', $e->getMessage(), "\n"; }
