package event

import "fmt"

type MetadataStore struct {
	data map[string]any
}

func NewMetadataStore() (*MetadataStore) {
	return &MetadataStore{data: make(map[string]any)}
}

func (m *MetadataStore) Set(key string, value any) {
	m.data[key] = value
}

func (m *MetadataStore) GetString(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("Key not found: %s", key)
	}
	s, ok := v.(string)
	if ok {
		return s, nil
	}
	return "", fmt.Errorf("Value is not a valid string")
}

func (m *MetadataStore) GetInt(key string) (int64, error) {
	v, ok := m.data[key]
	if !ok {
		return 0, fmt.Errorf("Key not found: %s", key)
	}
	i, ok := v.(int64)
	if ok {
		return i, nil
	} 
	return 0, fmt.Errorf("Value is not a valid int64")
}