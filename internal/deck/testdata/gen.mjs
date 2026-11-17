// Regenerates deck.txt with the web app's own sealDeck, so Go provably opens the TS format.
// Usage (from this folder): DECK_SECRET=test-secret node --experimental-strip-types gen.mjs <path-to-thai-song-quiz>
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const repo = process.argv[2];
const { sealDeck } = await import(pathToFileURL(join(repo, "src/lib/deck.ts")).href);
const questions = [0, 1].map((i) => ({
  id: `q${i}`,
  startAt: 5,
  answer: 2,
  track: { id: `t${i}`, title: `เพลงทดสอบ ${i}`, rawTitle: `เพลงทดสอบ ${i}`, artist: "วงทดสอบ", previewUrl: `https://example.com/${i}.m4a` },
  choices: [0, 1, 2, 3].map((c) => ({ title: `ตัวเลือก ${i}-${c}`, artist: "วงทดสอบ" })),
}));
writeFileSync(new URL("./deck.txt", import.meta.url), sealDeck(questions).deck);
