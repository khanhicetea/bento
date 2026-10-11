<?php
function db() {
    $m = new mysqli(getenv('DB_HOST'), getenv('DB_USERNAME'), getenv('DB_PASSWORD'), getenv('DB_DATABASE'), (int) getenv('DB_PORT'));
    $m->query('CREATE TABLE IF NOT EXISTS hits (id INT AUTO_INCREMENT PRIMARY KEY)');
    return $m;
}
