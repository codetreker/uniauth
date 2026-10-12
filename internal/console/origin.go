package console

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

func configuredOrigin(u *url.URL) (string, error) {
	host := u.Hostname()
	if strings.Contains(host, ":") || strings.HasPrefix(u.Host, "[") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return "", errors.New("public URL contains an invalid IPv6 host")
		}
		host = address.String()
		if address.Is4In6() {
			// Browser origin serialization keeps mapped addresses in the IPv6 family and uses hexadecimal pieces.
			value := address.As16()
			host = fmt.Sprintf("::ffff:%x:%x", uint16(value[12])<<8|uint16(value[13]), uint16(value[14])<<8|uint16(value[15]))
		}
	} else {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return "", fmt.Errorf("public URL contains an invalid domain: %w", err)
		}
		host = strings.ToLower(ascii)
		if err := validateNumericHost(host); err != nil {
			return "", err
		}
	}
	port := u.Port()
	if port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", errors.New("public URL port must be between zero and 65535")
		}
		port = strconv.FormatUint(number, 10)
		if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func validateNumericHost(host string) error {
	withoutTrailingDot := strings.TrimSuffix(host, ".")
	last := withoutTrailingDot[strings.LastIndex(withoutTrailingDot, ".")+1:]
	numeric := last != "" && strings.Trim(last, "0123456789") == ""
	hexadecimal := strings.HasPrefix(last, "0x") && strings.Trim(last[2:], "0123456789abcdef") == ""
	if numeric || hexadecimal {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is4() || address.String() != host {
			return errors.New("public URL numeric hosts must use canonical dotted decimal IPv4")
		}
	}
	return nil
}
