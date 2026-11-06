#!/bin/bash
# Build the e2e-laravel fixture (e2e-live.md section 8) and push it to the e2e Gitea.
set -euo pipefail
T=$(cat /root/.config/bento-test/gitea-token)
W=/root/lara; rm -rf $W; mkdir -p $W
docker run --rm -u 0:0 -e COMPOSER_HOME=/tmp/c -v $W:/w -w /w composer:2.8 \
  create-project --prefer-dist --no-interaction --no-scripts laravel/laravel e2e-laravel >/dev/null 2>&1
cd $W/e2e-laravel; rm -rf vendor
grep -o '"laravel/framework": "[^"]*"' composer.json
cat > database/migrations/2026_01_01_000000_create_e2e_tables.php <<'PHP'
<?php
use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;
return new class extends Migration {
    public function up(): void {
        Schema::create('visits', function (Blueprint $t) { $t->id(); $t->timestamp('processed_at')->nullable(); $t->timestamps(); });
        Schema::create('heartbeats', function (Blueprint $t) { $t->id(); $t->timestamps(); });
    }
    public function down(): void { Schema::dropIfExists('visits'); Schema::dropIfExists('heartbeats'); }
};
PHP
mkdir -p app/Jobs
cat > app/Jobs/ProcessVisit.php <<'PHP'
<?php
namespace App\Jobs;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Queue\Queueable;
use Illuminate\Support\Facades\DB;
class ProcessVisit implements ShouldQueue {
    use Queueable;
    public function __construct(public int $visitId) {}
    public function handle(): void { DB::table('visits')->where('id', $this->visitId)->update(['processed_at' => now()]); }
}
PHP
cat >> routes/console.php <<'PHP'

Artisan::command('app:heartbeat', function () {
    \Illuminate\Support\Facades\DB::table('heartbeats')->insert(['created_at' => now(), 'updated_at' => now()]);
})->purpose('e2e heartbeat');
\Illuminate\Support\Facades\Schedule::command('app:heartbeat')->everyMinute();
PHP
cat > routes/web.php <<'PHP'
<?php
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Route;
Route::get('/', function () {
    $id = DB::table('visits')->insertGetId(['created_at' => now(), 'updated_at' => now()]);
    \App\Jobs\ProcessVisit::dispatch($id);
    return ['mark' => config('app.e2e_mark'), 'visit' => $id];
});
Route::get('/status', fn () => [
    'mark' => config('app.e2e_mark'),
    'visits' => DB::table('visits')->count(),
    'processed' => DB::table('visits')->whereNotNull('processed_at')->count(),
    'pending' => DB::table('jobs')->count(),
    'failed' => DB::table('failed_jobs')->count(),
    'heartbeats' => DB::table('heartbeats')->count(),
    'cache' => cache()->increment('e2e-cache-hits'),
    'scheme' => request()->getScheme(),
]);
Route::get('/health', fn () => 'ok');
PHP
sed -i "0,/'name' =>/s//'e2e_mark' => 'lara-v1',\n\n    'name' =>/" config/app.php
grep -n e2e_mark config/app.php
curl -fsS -o /dev/null -H "Authorization: token $T" -H 'Content-Type: application/json' -X POST \
  http://127.0.0.1:3000/api/v1/user/repos -d '{"name":"e2e-laravel","private":true}' || true
git init -q -b main && git add -A && git -c user.name=e2e -c user.email=e2e@bento.invalid commit -qm "laravel fixture"
git push -q -f "http://e2e:$T@127.0.0.1:3000/e2e/e2e-laravel.git" main && echo pushed
