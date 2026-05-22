APP     = SSORefresher
BUNDLE  = $(APP).app
MACOS   = $(BUNDLE)/Contents/MacOS
BINARY  = $(MACOS)/$(APP)
LDFLAGS = -ldflags "-s -w"

.PHONY: build zip clean

build: $(BUNDLE)/Contents/Info.plist
	mkdir -p $(MACOS)
	CGO_ENABLED=1 GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY) .

$(BUNDLE)/Contents/Info.plist: Info.plist
	mkdir -p $(BUNDLE)/Contents
	cp Info.plist $(BUNDLE)/Contents/Info.plist

zip: build
	zip -r $(APP).zip $(BUNDLE)

clean:
	rm -rf $(BUNDLE) $(APP).zip
