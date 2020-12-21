# rclone-watchsync
This project implements watching a local directory and propagates any changes to the destination (see [related rclone issue](https://github.com/rclone/rclone/issues/249)). 

## Build and usage

**You can download the prebuilt linux binary in [Releases](https://github.com/antiops/rclone-watchsync/releases/)**

Try this by running
```
go build -i 
./rclone-watchsync watchsync source destination
```
similar to how `rclone sync` works. All rclone flags should work as expected.

Please don't use it on anything serious.

## Development
Short term
- Write tests
- Error handling / code correctness
- Decrease memory usage

Possible features
- More efficient processing (using purge, tracking moves)
- Mirroring directory structures
