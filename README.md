# Quick and dirty backup repository
Not the best or the cleanest but it was made quickly. Might refactor?

# Things to do before I can consider this "release worthy" (will get a tag when ready)
## Cleanup
- [x] Make things configurable and NOT hardcoded
- [x] Include all html files into the executable
- [ ] Create a descriptive README
- [x] Maybe move backend routes to `/api`
- [ ] Docker compose support for easy hosting (incl ENV_VAR config)
- [ ] Proper global RateLimit
- [ ] Clean up logging and add HTML logging
## Features
- [x] Timelapse GIF generator route. From {id1} to {id2} like diff. Consider adding FPS config with `?x=` params
    - fps can be set with the `?delay=` paremeter in 100th of a second (c(enti)s)
- [x] Infinite scrolling dynamic loading to not send user all data at once
- [ ] Homepage on `/` displaying a list of saved canvases and how many saves of each