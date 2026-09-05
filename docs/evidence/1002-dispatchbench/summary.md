# dispatchbench

- started: 2026-09-04T01:17:06.245613Z
- finished: 2026-09-04T01:20:01.868134Z
- workload: 10 turns per arm; turn i reads the i-th of 10 generated 40-line text files in the working directory and reports its line count and one word from it
- model: (the harness's own default)
- floor: abort below 2048MB free, at or above 1 swapouts per 2s sample, or above 2000MB in one worker tree

## Per turn

| arm | turn | wall s | cost USD | input | cache read | cache create | output | cached share | peak worker MB | peak host MB | min free MB |
|---|---|---|---|---|---|---|---|---|---|---|---|
| stateless | 1 | 6.5 | 0.3529 | 4 | 61014 | 31598 | 229 | 65.9% | 643 | 12859 | 7148 |
| stateless | 2 | 11.4 | 0.3770 | 6 | 111122 | 31159 | 368 | 78.1% | 646 | 12803 | 7060 |
| stateless | 3 | 14.4 | 0.3614 | 6 | 109544 | 29584 | 404 | 78.7% | 645 | 12790 | 7114 |
| stateless | 4 | 6.6 | 0.3237 | 4 | 63951 | 28661 | 179 | 69.0% | 641 | 13814 | 6745 |
| stateless | 5 | 7.0 | 0.3250 | 4 | 63951 | 28669 | 226 | 69.0% | 643 | 13861 | 7252 |
| stateless | 6 | 12.5 | 0.3770 | 6 | 111122 | 31159 | 368 | 78.1% | 646 | 13921 | 6543 |
| stateless | 7 | 7.1 | 0.3250 | 4 | 63951 | 28669 | 226 | 69.0% | 642 | 13691 | 7174 |
| stateless | 8 | 7.0 | 0.3237 | 4 | 63951 | 28661 | 180 | 69.0% | 637 | 13606 | 7213 |
| stateless | 9 | 7.6 | 0.3240 | 4 | 63951 | 28669 | 187 | 69.0% | 637 | 13907 | 7196 |
| stateless | 10 | 6.6 | 0.3239 | 4 | 63769 | 28487 | 261 | 69.1% | 642 | 14221 | 7042 |
| persistent | 1 | 6.0 | -- | 4 | 49739 | 51453 | 218 | 49.2% | 694 | 13557 | 7135 |
| persistent | 2 | 4.4 | -- | 4 | 103182 | 1768 | 114 | 98.3% | 711 | 13483 | 7136 |
| persistent | 3 | 4.4 | -- | 4 | 106732 | 1768 | 114 | 98.4% | 733 | 13615 | 7114 |
| persistent | 4 | 4.0 | -- | 4 | 110282 | 1768 | 114 | 98.4% | 750 | 14048 | 6989 |
| persistent | 5 | 3.8 | -- | 4 | 113832 | 1768 | 114 | 98.5% | 751 | 14172 | 6916 |
| persistent | 6 | 4.4 | -- | 3 | 117512 | 1897 | 114 | 98.4% | 756 | 14192 | 6968 |
| persistent | 7 | 4.1 | -- | 4 | 121190 | 1768 | 114 | 98.6% | 759 | 14254 | 7009 |
| persistent | 8 | 4.4 | -- | 4 | 124740 | 1768 | 114 | 98.6% | 760 | 14466 | 7038 |
| persistent | 9 | 3.8 | -- | 4 | 128290 | 1768 | 114 | 98.6% | 762 | 14472 | 6967 |
| persistent | 10 | 3.8 | -- | 4 | 131840 | 1768 | 114 | 98.7% | 763 | 14428 | 6927 |

## Per arm

| arm | turns answered | total cost USD | mean wall s | mean cached share | peak worker MB | token source |
|---|---|---|---|---|---|---|
| stateless | 10 | 3.4136 (over 10 of 10 turns) | 8.7 | 71.5% | 646 | claude -p --output-format json envelope |
| persistent | 10 | -- | 4.3 | 93.6% | 763 | Claude Code session transcript |

## Host, over the whole run

- peak host-wide resident: 14490MB across 265 samples
- minimum free memory seen: 6543MB (floor was 2048MB)
- maximum swapouts in any 2s sample: 0 (limit was 1)

## The persistent lane's own /cost report

```
Total cost:            $1.56
Total duration (API):  53s
Total duration (wall): 1m 6s
Total code changes:    0 lines added, 0 lines removed
Usage by model:
claude-haiku-4-5:  1.1k input, 32 output, 0 cache read, 0 cache write ($0.0013)
claude-opus-5:  4.6k input, 1.6k output, 1.6m cache read, 67.7k cache write ($1.56)
```
