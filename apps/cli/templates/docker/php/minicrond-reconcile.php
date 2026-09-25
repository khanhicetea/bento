<?php
// Bento-owned housekeeping only. The registry and all non-reserved definitions
// belong to the app; never delete them during render or runner restart.
$socket = getenv('MINICRON_DATA') . '/minicron.sock';
$basePath = rtrim(getenv('BASE_PATH') ?: '', '/');
$expectedPath = $argv[1] ?? '';
try {
    $expected = json_decode(file_get_contents($expectedPath), true, 64, JSON_THROW_ON_ERROR);
    if (!is_array($expected) || !array_is_list($expected)) {
        throw new RuntimeException('invalid internal task allowlist');
    }
    foreach ($expected as $name) {
        if (!is_string($name) || !preg_match('/^bento-internal-[a-z0-9_.-]+$/D', $name)) {
            throw new RuntimeException('invalid internal task name');
        }
    }
    $request = static function (string $method, string $path) use ($socket, $basePath): array {
        $handle = curl_init('http://minicron' . $basePath . '/api/v1/' . $path);
        if ($handle === false) throw new RuntimeException('cannot open scheduler request');
        $response = '';
        curl_setopt_array($handle, [
            CURLOPT_UNIX_SOCKET_PATH => $socket,
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_TIMEOUT => 10,
            CURLOPT_WRITEFUNCTION => static function ($handle, $chunk) use (&$response): int {
                if (strlen($response) + strlen($chunk) > 1048576) return 0;
                $response .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($handle);
        $status = curl_getinfo($handle, CURLINFO_RESPONSE_CODE);
        curl_close($handle);
        if ($ok === false || $status < 200 || $status >= 300) {
            throw new RuntimeException('scheduler reconciliation request failed');
        }
        return $response === '' ? [] : json_decode($response, true, 64, JSON_THROW_ON_ERROR);
    };
    $items = $request('GET', 'jobs')['items'] ?? null;
    if (!is_array($items)) throw new RuntimeException('invalid scheduler list response');
    foreach ($items as $item) {
        $name = $item['name'] ?? null;
        if (!is_string($name)) throw new RuntimeException('invalid scheduler definition');
        if (str_starts_with($name, 'bento-internal-') && !in_array($name, $expected, true)) {
            $request('DELETE', 'jobs/' . rawurlencode($name));
        }
    }
} catch (Throwable $error) {
    // Deliberately do not emit HTTP bodies or credentials from a failed request.
    fwrite(STDERR, "minicrond internal reconciliation failed\n");
    exit(1);
}
