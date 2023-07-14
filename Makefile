.PHONY: run-docker

run-docker:
	docker run -d -p 2222:22 -v $(shell pwd):/home/foo/upload --name=sftp_test_server atmoz/sftp foo:pass:::upload
