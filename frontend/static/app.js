const localVideo = document.getElementById('localVideo');
const remoteVideo = document.getElementById('remoteVideo');
const roomIdElement = document.getElementById('roomId')
const roomIdInputElement = document.getElementById('roomIdInput')
const statsElemnt = document.getElementById('stats')


const startButton = document.getElementById('startButton');
const toggleCameraButton = document.getElementById('toggleCameraButton');
const toggleMicButton = document.getElementById('toggleMicButton');

let peerConnection;
let localStream = null;
let signaling;
let cameraEnabled = true;
let micEnabled = true;
let roomId;
let started = false;
let userId = null;
var logConEst = false;
var iceCandBuff = [];

async function startMedia() {
    let constraints = {
        video: false, audio: false
    }
    try {
        let devices = await navigator.mediaDevices.enumerateDevices()
        devices.forEach(function (dev) {
            if (dev.kind === "videoinput") {
                constraints.video = true
            } else if (dev.kind === "audioinput") {
                constraints.audio = true
            }
        })
    } catch (error) {
        console.log("Get devices error " + error)
        return
    }

    try {
        
        localStream = await navigator.mediaDevices.getUserMedia(constraints);
        localVideo.srcObject = localStream;
        remoteVideo.srcObject = localStream;
    } catch (error) {
        console.error('Ошибка доступа к медиа устройствам:', error);
        if (error.message.includes("Could not start video source")){
            console.log("Trying start only audio")
            constraints.video = false
            localStream = await navigator.mediaDevices.getUserMedia(constraints);
            localVideo.srcObject = localStream;
        }else{

            return
        }
        
    }
    setCam(cameraEnabled)
    setMic(cameraEnabled)
    setStartButton(started)
}

async function createOffer() {
    if (peerConnection.signalingState === "stable") {
        const offer = await peerConnection.createOffer();
        await peerConnection.setLocalDescription(offer);
        signaling.send(JSON.stringify({
            event: "offer",
            data: offer
        }));
    } else {
        console.warn("Неожиданное состояние для создания предложения:", peerConnection.signalingState);
    }
}

startButton.addEventListener('click', async () => {
    if (started) {
        startButton.disabled = true
        await hangup()
        signaling.send(JSON.stringify({
            event: "bye",
            data: userId
        }));
        started = false
        setStartButton(started)
        startButton.disabled = false
    } else {
        startButton.disabled = true
        await startCall()
        started = true
        setStartButton(started)
        startButton.disabled = false
    }
});

async function hangup() {
    console.log("hangup")
    if (peerConnection) {
        peerConnection.close();
        peerConnection = null;
    }
    if (localStream != null) {
        localStream.getTracks().forEach(track => track.stop());
        localStream = null;
    }
    console.log("reload")
    // window.location.reload()
}

async function procBuff(){
    iceCandBuff.forEach(async (d) => {await peerConnection.addIceCandidate(new RTCIceCandidate(d))})
}

async function startCall() {
    var wsHost = window.location.host;
    let roomId = roomIdInputElement.value;
    if (roomId == '') {
        roomId = crypto.randomUUID()
        console.log('Room id: ' + roomId)
    }
    roomIdElement.textContent = roomId
    var url
    if (window.location.protocol == 'https:'){
        url = 'wss://' + wsHost + '/ws/rooms/' + roomId
        
    }else{
        url = 'ws://' + wsHost + ':8080/ws/rooms/' + roomId
    }
    signaling = new WebSocket(url);
    const configuration = {
        iceServers: [
            {url:'stun:stun.l.google.com:19302'},
            {url:'stun:stun1.l.google.com:19302'},
            {url:'stun:stun2.l.google.com:19302'},
            {url:'stun:stun3.l.google.com:19302'},
            {url:'stun:stun4.l.google.com:19302'},
            {url:'stun:stunserver.org'},
        ]
    }
    peerConnection = new RTCPeerConnection(configuration);

    if (localStream != null) {
        localStream.getTracks().forEach(track => peerConnection.addTrack(track, localStream));
    }

    peerConnection.ontrack = (event) => {
        console.log("ON TRACK: " + event.streams.length);
        console.log("TRACK: " + event.streams[0]);
        remoteVideo.srcObject = event.streams[0];
        console.log(
            "SRC OBJ: " +   remoteVideo.srcObject
        )

        remoteVideo.srcObject = event.streams[0];
    };

    peerConnection.onicecandidate = (event) => {
        if (event.candidate) {
            signaling.send(JSON.stringify({
                event: "candidate",
                data: event.candidate
            }));
        }
    };

    peerConnection.onconnectionstatechange = (event) => {
        console.log("Connection state changed: " + JSON.stringify(event) + " | " + peerConnection.iceConnectionState)
        if (peerConnection.iceConnectionState === 'disconnected') {
            hangup();
        }
    };
    signaling.onmessage = async (message) => {
        const data = JSON.parse(message.data);
        if (!data) return;
        if (data.event === "answer" && data.data) {
            console.log("получен answer, " + peerConnection.signalingState)
            if (peerConnection.signalingState === "have-local-offer") {
                await peerConnection.setRemoteDescription(data.data);
                logConEst = true;
                await procBuff()
            } else {
                console.warn("Неожиданное состояние для установки ответа:", peerConnection.signalingState);
            }
        } else if (data.event === "offer" && data.data) {
            console.log("получен offer " + peerConnection.signalingState)
            if (peerConnection.signalingState === "stable") {
                console.log("OFFER")
                console.log(data.data)
                var r = await peerConnection.setRemoteDescription(data.data);
                console.log('set remote desc result: ' + r);
                var remoteStreams = peerConnection.getRemoteStreams();
                console.log("remoteStreams.length " + remoteStreams.length)
                console.log("remoteStreams[0].getVideoTracks().length " + remoteStreams[0].getVideoTracks().length)

                const answer = await peerConnection.createAnswer();
                await peerConnection.setLocalDescription(answer);
                signaling.send(JSON.stringify({
                    event: "answer",
                    data: answer
                }));
                logConEst = true
                await procBuff()
            } else {
                console.warn("Неожиданное состояние для установки предложения:", peerConnection.signalingState);
            }
        } else if (data.event == "candidate" && data.data) {
            console.log("ICE CANDIDATE , state:" + peerConnection.signalingState)
            if (logConEst){
                if (peerConnection.signalingState !== "closed") {
                    console.log("ADD ice candidate")
                    await peerConnection.addIceCandidate(new RTCIceCandidate(data.data));
                }           
            }else{
                iceCandBuff.push(data.data)
            }
        } else if (data.event === "user_joined") {
            await createOffer();
        } else if (data.event === "error") {
            console.error(data);
        } else {
            onEventCallback(data);
        }
    };
}


toggleCameraButton.addEventListener('click', () => {
    cameraEnabled = !cameraEnabled;
    setCam(cameraEnabled)
});


toggleMicButton.addEventListener('click', () => {
    micEnabled = !micEnabled;
    setMic(micEnabled)
});

function setMic(enabled) {
    localStream.getAudioTracks()[0].enabled = enabled;
    toggleMicButton.textContent = enabled ? 'Disable Microphone' : 'Enable Microphone';
}

function setCam(enabled) {
    localStream.getVideoTracks()[0].enabled = enabled;
    toggleCameraButton.textContent = enabled ? 'Disable Camera' : 'Enable Camera';
}

function setStartButton(started) {
    startButton.textContent = started ? 'Hangup' : 'Call';
}


startMedia();

setInterval(() => {
    if (!peerConnection) {
        return
    }
    peerConnection.getStats(null).then((stats) => {
        let statsOutput = "";

        stats.forEach((report) => {
            statsOutput +=
                `<h2>Report: ${report.type}</h2>\n<strong>ID:</strong> ${report.id}<br>\n` +
                `<strong>Timestamp:</strong> ${report.timestamp}<br>\n`;

            // Now the statistics for this report; we intentionally drop the ones we
            // sorted to the top above

            Object.keys(report).forEach((statName) => {
                if (
                    statName !== "id" &&
                    statName !== "timestamp" &&
                    statName !== "type"
                ) {
                    statsOutput += `<strong>${statName}:</strong> ${report[statName]}<br>\n`;
                }
            });
        });

        document.getElementById("stats").innerHTML = statsOutput;
    });
}, 1000);

async function onEventCallback(data) {
    console.log("Event callback " + JSON.stringify(data));
    if (data.event === "connection") {
        userId = data.data.user_id
    } else if (data.event === "bye") {
        // startButton.disabled = true
        // await hangup()
        // signaling.send(JSON.stringify({
        //     event: "bye",
        //     data: userId
        // }));
        // started = false
        // setStartButton(started)
        // startButton.disabled = false
    }

} 
