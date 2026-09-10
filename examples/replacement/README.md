# replacement: a git that is only what the guardfile grants

Every other example adds a guarded verb to a binary of its own. This one takes the binary away: `umbra install` writes the generated CLI onto a PATH directory under the name `git`, so a caller runs `git status` and never learns umbra is there.

There is no Go here. The whole example is [one guardfile](.umbra/git.guardfile.kdl).

## Run it

```sh
cd examples/replacement
umbra lock                                  # freezes the umbra module version
umbra install --shim-dir ./shims
PATH="$PWD/shims:$PATH"
```

`lock` is separate because `specverb.lock` pins the umbra module version, which is yours rather than this repository's. An exec member commits no other lock.

## What it shows

```
$ git --help
NAME:
   git - git, occluded by umbra to the verbs example git grants

COMMANDS:
   status  exec: git status
   log     exec: git log
   diff    exec: git diff
   commit  exec: git commit
   push    NOT AVAILABLE - withheld by policy. This example occludes a local git: nothing here should reach a remote. Use `status` instead.
```

Four verbs from git's hundred and forty, one stated refusal, and no sign of the rest.

```sh
$ git status --short          # runs the real git, and writes an audit row
$ git commit --no-verify -m x # exit 5: the flag is denied for this grant
$ git push                    # exit 2: withheld, and it says why
$ git rebase                  # exit 2: not granted, and it does not say which
$ UMBRA_IDENTIFY=1 git        # the only surface that admits umbra is here
```

## What it does not show

The real git is still one absolute path away, and this example cannot stop that. A replacement is what a caller sees rather than what a caller can reach. See [occluded replacement binaries](../../docs/execverb-replacement.md) for the enforcement floor umbra does not own.

## Clean up

```sh
rm -rf shims
```
