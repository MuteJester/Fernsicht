# snakemake example

Snakemake emits per-step progress like
`[5 of 11 steps (45%) done]`. Tier-1 auto-detection catches it via
the `fraction-of` parser, but a project-local `.fernsicht.toml`
gives cleaner labels + tighter pattern matching.

## Run

Assuming you have snakemake + a Snakefile in this directory:

```bash
fernsicht run -- snakemake --cores 4
```

The bundled `.fernsicht.toml` contributes:

```toml
[run]
default_label = "snakemake"
default_unit  = "step"

[[detection.patterns]]
name  = "snakemake"
regex = '\[(\d+) of (\d+) steps \((\d+)%\) done\]'
n_capture     = 1
total_capture = 2
```

Now ticks fire with `n` + `total` (computed `value = n/total`),
labeled "snakemake", unit "step". Viewers see a clean labeled bar.

## With magic prefix instead

If you'd rather drive lifecycle explicitly per Snakemake rule, add a
shell wrapper to your rule:

```python
# In your Snakefile:
rule align:
    input: "data/{sample}.fastq"
    output: "aligned/{sample}.bam"
    shell:
        '''
        echo '__fernsicht__ start "align {wildcards.sample}"'
        actual_align_command
        echo '__fernsicht__ end'
        '''
```

The bridge implicitly ends any previous task on each new `start`.

## Verifying

Run with `--debug` to see the parser's view:

```bash
fernsicht run --debug -- snakemake --cores 1 --dry-run
# ... look for `[parse] custom:snakemake n=5 total=11 ...`
```
