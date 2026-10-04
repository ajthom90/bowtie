// Runs transpiled BrightScript (out/staging, from bsc) under the
// @rokucommunity/brs interpreter so pure library logic can be exercised in
// node. Not a test file itself (no .test. in the name).
//
//   runBrs(['source/lib/Favorites.brs'], `
//       out.sorted = bowtie_favorites_sortChannels([...])
//   `)
//
// The body runs inside `sub main()` with an `out` AA in scope; whatever it
// puts there comes back JSON-decoded. Namespaced functions are called by
// their transpiled names (bowtie.favorites.x → bowtie_favorites_x).
// Two BrightScript quirks to write around: `out.fooBar = x` stores the key
// as "foobar" (use lowercase output names), and FormatJson refuses an object
// that appears twice in `out` (store snapshot(x), a deep copy, instead).
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const root = new URL('../', import.meta.url).pathname;
const staging = join(root, 'out/staging');
// brs scans components/ under its cwd; run it where there is none.
const workDir = join(root, 'out/brs');
const brsBin = join(root, 'node_modules/.bin/brs');
const SENTINEL = '@@BRS_OUT ';

let seq = 0;

export function runBrs(libs, body) {
    const sources = libs.map((lib) => {
        const p = join(staging, lib);
        if (!existsSync(p)) throw new Error(`${lib} missing from out/staging (run bsc first)`);
        return readFileSync(p, 'utf8');
    });
    const main = [
        'sub main()',
        '    out = {}',
        body,
        `    print "${SENTINEL}" + FormatJson(out)`,
        'end sub',
        'function snapshot(x as dynamic) as dynamic',
        '    return ParseJson(FormatJson(x))',
        'end function',
    ].join('\n');

    mkdirSync(workDir, { recursive: true });
    const file = join(workDir, `run-${process.pid}-${seq++}.brs`);
    writeFileSync(file, [...sources, main].join('\n'));

    let stdout;
    try {
        stdout = execFileSync(brsBin, [file], { cwd: workDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    } catch (err) {
        throw new Error(`brs failed (${file}):\n${err.stdout ?? ''}${err.stderr ?? ''}`);
    }
    rmSync(file);
    const line = stdout.split(/\r?\n/).reverse().find((l) => l.startsWith(SENTINEL));
    const json = line?.slice(SENTINEL.length);
    if (!json) throw new Error(`brs produced no result:\n${stdout}`);
    return JSON.parse(json);
}
