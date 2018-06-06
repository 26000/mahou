## mahou
a \[matrix] webRTC echo bot. you call it, say something and hear it back. intended for
testing voice calling.

### setup
```
$ go get -u github.com/26000/mahou
$ mahou
 mahou 2018/06/06 20:13:51 v0.0.1 is booting up! https://github.com/26000/mahou
 mahou 2018/06/06 20:13:51 a configuration file was created at mahou.conf; insert your settings there and launch mahou again
$ vim mahou.conf
$ mahou
```

you just install it as a usual golang package, run, edit the newly created
config (the filepath can be specified with `-c` flag) and run it again. the
config has nice comments describing everything.

have fun!

### constraints
if you don't have working webRTC set up on your \[matrix] homeserver, it
won't work obviously. if you're behind nat, the bot is behind a different nat
and your coturn server is only configured to use stun (without turn), you will
probably be unable to call it.

doesn't support windows due to go-webrtc requirements.

doesn't support video calls (yet) due to go-webrtc requirements.

### help
PRs are welcome. don't forget to create a feature branch prior to opening the PR.

