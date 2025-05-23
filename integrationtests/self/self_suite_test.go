package self_test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/For-ACGN/quic-go"
	"github.com/For-ACGN/quic-go/internal/utils"
	"github.com/For-ACGN/quic-go/logging"
	"github.com/For-ACGN/quic-go/metrics"
	"github.com/For-ACGN/quic-go/qlog"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const alpn = "quic-go integration tests"

const (
	dataLen     = 500 * 1024       // 500 KB
	dataLenLong = 50 * 1024 * 1024 // 50 MB
)

var (
	// PRData contains dataLen bytes of pseudo-random data.
	PRData = GeneratePRData(dataLen)
	// PRDataLong contains dataLenLong bytes of pseudo-random data.
	PRDataLong = GeneratePRData(dataLenLong)
)

// See https://en.wikipedia.org/wiki/Lehmer_random_number_generator
func GeneratePRData(l int) []byte {
	res := make([]byte, l)
	seed := uint64(1)
	for i := 0; i < l; i++ {
		seed = seed * 48271 % 2147483647
		res[i] = byte(seed)
	}
	return res
}

const logBufSize = 100 * 1 << 20 // initial size of the log buffer: 100 MB

type syncedBuffer struct {
	mutex sync.Mutex

	*bytes.Buffer
}

func (b *syncedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	n, err := b.Buffer.Write(p)
	b.mutex.Unlock()
	return n, err
}

func (b *syncedBuffer) Bytes() []byte {
	b.mutex.Lock()
	p := b.Buffer.Bytes()
	b.mutex.Unlock()
	return p
}

func (b *syncedBuffer) Reset() {
	b.mutex.Lock()
	b.Buffer.Reset()
	b.mutex.Unlock()
}

var (
	logFileName   string // the log file set in the ginkgo flags
	logBufOnce    sync.Once
	logBuf        *syncedBuffer
	enableQlog    bool
	enableMetrics bool

	tlsConfig          *tls.Config
	tlsConfigLongChain *tls.Config
	tlsClientConfig    *tls.Config
	tracer             logging.Tracer
)

// read the logfile command line flag
// to set call ginkgo -- -logfile=log.txt
func init() {
	flag.StringVar(&logFileName, "logfile", "", "log file")
	flag.BoolVar(&enableQlog, "qlog", false, "enable qlog")
	// metrics won't be accessible anywhere, but it's useful to exercise the code
	flag.BoolVar(&enableMetrics, "metrics", false, "enable metrics")
}

var _ = BeforeSuite(func() {
	mrand.Seed(GinkgoRandomSeed())

	ca, caPrivateKey, err := generateCA()
	if err != nil {
		panic(err)
	}
	leafCert, leafPrivateKey, err := generateLeafCert(ca, caPrivateKey)
	if err != nil {
		panic(err)
	}
	tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafCert.Raw},
			PrivateKey:  leafPrivateKey,
		}},
		NextProtos: []string{alpn},
	}
	tlsConfLongChain, err := generateTLSConfigWithLongCertChain(ca, caPrivateKey)
	if err != nil {
		panic(err)
	}
	tlsConfigLongChain = tlsConfLongChain

	root := x509.NewCertPool()
	root.AddCert(ca)
	tlsClientConfig = &tls.Config{
		RootCAs:    root,
		NextProtos: []string{alpn},
	}

	var qlogTracer, metricsTracer logging.Tracer
	if enableQlog {
		qlogTracer = qlog.NewTracer(func(p logging.Perspective, connectionID []byte) io.WriteCloser {
			role := "server"
			if p == logging.PerspectiveClient {
				role = "client"
			}
			filename := fmt.Sprintf("log_%x_%s.qlog", connectionID, role)
			fmt.Fprintf(GinkgoWriter, "Creating %s.\n", filename)
			f, err := os.Create(filename)
			Expect(err).ToNot(HaveOccurred())
			bw := bufio.NewWriter(f)
			return utils.NewBufferedWriteCloser(bw, f)
		})
	}
	if enableMetrics {
		metricsTracer = metrics.NewTracer()
	}

	if enableQlog && enableMetrics {
		tracer = logging.NewMultiplexedTracer(qlogTracer, metricsTracer)
	} else if enableQlog {
		tracer = qlogTracer
	} else if enableMetrics {
		tracer = metricsTracer
	}
})

func generateCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	certTempl := &x509.Certificate{
		SerialNumber:          big.NewInt(2019),
		Subject:               pkix.Name{},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, certTempl, certTempl, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(caBytes)
	if err != nil {
		return nil, nil, err
	}
	return ca, caPrivateKey, nil
}

func generateLeafCert(ca *x509.Certificate, caPrivateKey *rsa.PrivateKey) (*x509.Certificate, *rsa.PrivateKey, error) {
	certTempl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, certTempl, ca, &privKey.PublicKey, caPrivateKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, privKey, nil
}

// getTLSConfigWithLongCertChain generates a tls.Config that uses a long certificate chain.
// The Root CA used is the same as for the config returned from getTLSConfig().
func generateTLSConfigWithLongCertChain(ca *x509.Certificate, caPrivateKey *rsa.PrivateKey) (*tls.Config, error) {
	const chainLen = 7
	certTempl := &x509.Certificate{
		SerialNumber:          big.NewInt(2019),
		Subject:               pkix.Name{},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	lastCA := ca
	lastCAPrivKey := caPrivateKey
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	certs := make([]*x509.Certificate, chainLen)
	for i := 0; i < chainLen; i++ {
		caBytes, err := x509.CreateCertificate(rand.Reader, certTempl, lastCA, &privKey.PublicKey, lastCAPrivKey)
		if err != nil {
			return nil, err
		}
		ca, err := x509.ParseCertificate(caBytes)
		if err != nil {
			return nil, err
		}
		certs[i] = ca
		lastCA = ca
		lastCAPrivKey = privKey
	}
	leafCert, leafPrivateKey, err := generateLeafCert(lastCA, lastCAPrivKey)
	if err != nil {
		return nil, err
	}

	rawCerts := make([][]byte, chainLen+1)
	for i, cert := range certs {
		rawCerts[chainLen-i] = cert.Raw
	}
	rawCerts[0] = leafCert.Raw

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: rawCerts,
			PrivateKey:  leafPrivateKey,
		}},
		NextProtos: []string{alpn},
	}, nil
}

func getTLSConfig() *tls.Config {
	return tlsConfig.Clone()
}

func getTLSConfigWithLongCertChain() *tls.Config {
	return tlsConfigLongChain.Clone()
}

func getTLSClientConfig() *tls.Config {
	return tlsClientConfig.Clone()
}

func getQuicConfig(conf *quic.Config) *quic.Config {
	if conf == nil {
		conf = &quic.Config{}
	} else {
		conf = conf.Clone()
	}
	conf.Tracer = tracer
	return conf
}

var _ = BeforeEach(func() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	if debugLog() {
		logBufOnce.Do(func() {
			logBuf = &syncedBuffer{Buffer: bytes.NewBuffer(make([]byte, 0, logBufSize))}
		})
		utils.DefaultLogger.SetLogLevel(utils.LogLevelDebug)
		log.SetOutput(logBuf)
	}
})

var _ = AfterEach(func() {
	if debugLog() {
		logFile, err := os.Create(logFileName)
		Expect(err).ToNot(HaveOccurred())
		logFile.Write(logBuf.Bytes())
		logFile.Close()
		logBuf.Reset()
	}
})

// Debug says if this test is being logged
func debugLog() bool {
	return len(logFileName) > 0
}

func scaleDuration(d time.Duration) time.Duration {
	scaleFactor := 1
	if f, err := strconv.Atoi(os.Getenv("TIMESCALE_FACTOR")); err == nil { // parsing "" errors, so this works fine if the env is not set
		scaleFactor = f
	}
	Expect(scaleFactor).ToNot(BeZero())
	return time.Duration(scaleFactor) * d
}

func TestSelf(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Self integration tests")
}
