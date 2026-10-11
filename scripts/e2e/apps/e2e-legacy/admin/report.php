<?php
require __DIR__ . '/../db.php';
echo json_encode(array('page' => 'admin/report', 'php' => PHP_VERSION, 'mysqli' => db()->server_info)), "\n";
