run-front:
	docker run -p 80:80 -v ${PWD}/frontend/static:/usr/share/nginx/html nginx:alpine

build:
	docker build -t vicos-api .

run:
	docker run -p 8080:8080 vicos-api